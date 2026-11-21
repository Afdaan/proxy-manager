package model

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	// ponytail: MVP accepts ASCII FQDNs only; add IDNA and wildcard validation with DNS-01 support.
	domainPattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)
	countryPattern  = regexp.MustCompile(`^[A-Z]{2}$`)
	revisionPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
)

type GatewayConfig struct {
	Generation uint64 `json:"generation"`
	Revision   string `json:"revision"`
	Sites      []Site `json:"sites"`
}

type Site struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Enabled         bool              `json:"enabled"`
	Domains         []string          `json:"domains"`
	Upstreams       []Upstream        `json:"upstreams"`
	LoadBalancing   string            `json:"loadBalancing"`
	TLS             TLSPolicy         `json:"tls"`
	RateLimit       RateLimitPolicy   `json:"rateLimit"`
	WAF             WAFPolicy         `json:"waf"`
	Geo             GeoPolicy         `json:"geo"`
	Access          AccessPolicy      `json:"access"`
	SecurityHeaders bool              `json:"securityHeaders"`
	MaxBodySizeMB   int               `json:"maxBodySizeMb"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type Upstream struct {
	URL       string `json:"url"`
	Weight    int    `json:"weight"`
	MaxFails  int    `json:"maxFails"`
	FailAfter int    `json:"failAfterSeconds"`
	Backup    bool   `json:"backup"`
}

type TLSPolicy struct {
	Mode            string `json:"mode"`
	Email           string `json:"email,omitempty"`
	Ready           bool   `json:"ready"`
	Status          string `json:"status,omitempty"`
	Error           string `json:"error,omitempty"`
	CertificatePath string `json:"certificatePath,omitempty"`
	PrivateKeyPath  string `json:"privateKeyPath,omitempty"`
	RedirectHTTP    bool   `json:"redirectHttp"`
}

type RateLimitPolicy struct {
	Enabled       bool `json:"enabled"`
	Requests      int  `json:"requests"`
	PeriodSeconds int  `json:"periodSeconds"`
	Burst         int  `json:"burst"`
	RejectStatus  int  `json:"rejectStatus"`
	NoDelay       bool `json:"noDelay"`
}

type WAFPolicy struct {
	Mode            string   `json:"mode"`
	ParanoiaLevel   int      `json:"paranoiaLevel"`
	ExcludedRuleIDs []string `json:"excludedRuleIds,omitempty"`
}

type GeoPolicy struct {
	Mode      string   `json:"mode"`
	Countries []string `json:"countries,omitempty"`
}

type AccessPolicy struct {
	AllowedCIDRs []string `json:"allowedCidrs,omitempty"`
	DeniedCIDRs  []string `json:"deniedCidrs,omitempty"`
}

func (config *GatewayConfig) Validate() error {
	if config.Generation == 0 {
		return errors.New("generation must be greater than zero")
	}
	if !revisionPattern.MatchString(config.Revision) {
		return errors.New("revision must contain only letters, numbers, dots, underscores, and dashes")
	}

	domains := make(map[string]string)
	ids := make(map[string]struct{})
	for index := range config.Sites {
		site := &config.Sites[index]
		if err := site.Validate(); err != nil {
			return fmt.Errorf("site %q: %w", site.ID, err)
		}
		if _, exists := ids[site.ID]; exists {
			return fmt.Errorf("duplicate site id %q", site.ID)
		}
		ids[site.ID] = struct{}{}

		for _, domain := range site.Domains {
			if owner, exists := domains[domain]; exists {
				return fmt.Errorf("domain %q is already assigned to site %q", domain, owner)
			}
			domains[domain] = site.ID
		}
	}

	return nil
}

func (site *Site) Validate() error {
	site.ID = strings.ToLower(strings.TrimSpace(site.ID))
	site.Name = strings.TrimSpace(site.Name)
	if !idPattern.MatchString(site.ID) {
		return errors.New("id must be a lowercase slug")
	}
	if site.Name == "" || len(site.Name) > 120 {
		return errors.New("name must contain 1 to 120 characters")
	}
	if len(site.Domains) == 0 {
		return errors.New("at least one domain is required")
	}
	primaryDomain, err := NormalizeDomain(site.Domains[0])
	if err != nil {
		return err
	}
	for index, domain := range site.Domains {
		domain, err := NormalizeDomain(domain)
		if err != nil {
			return err
		}
		site.Domains[index] = domain
	}
	slices.Sort(site.Domains)
	site.Domains = slices.Compact(site.Domains)
	aliases := make([]string, 0, len(site.Domains)-1)
	for _, domain := range site.Domains {
		if domain != primaryDomain {
			aliases = append(aliases, domain)
		}
	}
	site.Domains = append([]string{primaryDomain}, aliases...)

	if site.Enabled && len(site.Upstreams) == 0 {
		return errors.New("enabled sites require at least one upstream")
	}
	upstreamScheme := ""
	for index := range site.Upstreams {
		scheme, err := site.Upstreams[index].Validate()
		if err != nil {
			return fmt.Errorf("upstream %d: %w", index+1, err)
		}
		if upstreamScheme != "" && upstreamScheme != scheme {
			return errors.New("all upstreams in one site must use the same scheme")
		}
		upstreamScheme = scheme
	}

	if site.LoadBalancing == "" {
		site.LoadBalancing = "round_robin"
	}
	if !slices.Contains([]string{"round_robin", "least_conn", "ip_hash"}, site.LoadBalancing) {
		return errors.New("loadBalancing must be round_robin, least_conn, or ip_hash")
	}
	if site.LoadBalancing == "ip_hash" {
		for _, upstream := range site.Upstreams {
			if upstream.Backup {
				return errors.New("backup upstreams are not supported with ip_hash")
			}
		}
	}
	if err := site.TLS.Validate(); err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	if err := site.RateLimit.Validate(); err != nil {
		return fmt.Errorf("rate limit: %w", err)
	}
	if err := site.WAF.Validate(); err != nil {
		return fmt.Errorf("waf: %w", err)
	}
	if err := site.Geo.Validate(); err != nil {
		return fmt.Errorf("geo: %w", err)
	}
	if err := site.Access.Validate(); err != nil {
		return fmt.Errorf("access: %w", err)
	}
	if site.MaxBodySizeMB == 0 {
		site.MaxBodySizeMB = 10
	}
	if site.MaxBodySizeMB < 1 || site.MaxBodySizeMB > 10_240 {
		return errors.New("maxBodySizeMb must be between 1 and 10240")
	}

	return nil
}

func NormalizeDomain(value string) (string, error) {
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if !domainPattern.MatchString(domain) {
		return "", fmt.Errorf("invalid domain %q", domain)
	}
	return domain, nil
}

func (upstream *Upstream) Validate() (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(upstream.URL))
	if err != nil {
		return "", errors.New("invalid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("URL scheme must be http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("URL must contain only scheme, host, and port")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("upstream base paths are not supported yet")
	}
	if upstream.Weight == 0 {
		upstream.Weight = 1
	}
	if upstream.Weight < 1 || upstream.Weight > 100 {
		return "", errors.New("weight must be between 1 and 100")
	}
	if upstream.MaxFails == 0 {
		upstream.MaxFails = 3
	}
	if upstream.MaxFails < 1 || upstream.MaxFails > 100 {
		return "", errors.New("maxFails must be between 1 and 100")
	}
	if upstream.FailAfter == 0 {
		upstream.FailAfter = 10
	}
	if upstream.FailAfter < 1 || upstream.FailAfter > 3600 {
		return "", errors.New("failAfterSeconds must be between 1 and 3600")
	}
	upstream.URL = parsed.String()
	return parsed.Scheme, nil
}

func (policy *TLSPolicy) Validate() error {
	if policy.Mode == "" {
		policy.Mode = "off"
	}
	if !slices.Contains([]string{"off", "automatic", "custom"}, policy.Mode) {
		return errors.New("mode must be off, automatic, or custom")
	}
	if policy.Mode == "automatic" {
		address, err := mail.ParseAddress(policy.Email)
		if err != nil || address.Address != policy.Email {
			return errors.New("automatic TLS requires a valid email")
		}
	}
	if policy.Status != "" && !slices.Contains([]string{"pending", "issuing", "active", "failed"}, policy.Status) {
		return errors.New("status must be pending, issuing, active, or failed")
	}
	if len(policy.Error) > 2000 {
		return errors.New("certificate error must not exceed 2000 characters")
	}
	if policy.Mode == "custom" && (policy.CertificatePath == "" || policy.PrivateKeyPath == "") {
		return errors.New("custom TLS requires certificatePath and privateKeyPath")
	}
	for _, path := range []string{policy.CertificatePath, policy.PrivateKeyPath} {
		if path != "" && (!strings.HasPrefix(path, "/") || strings.Contains(path, "..")) {
			return errors.New("certificate paths must be absolute and cannot contain parent traversal")
		}
	}
	return nil
}

func (policy *RateLimitPolicy) Validate() error {
	if !policy.Enabled {
		return nil
	}
	if policy.Requests < 1 || policy.Requests > 1_000_000 {
		return errors.New("requests must be between 1 and 1000000")
	}
	if policy.PeriodSeconds != 1 && policy.PeriodSeconds != 60 {
		return errors.New("periodSeconds must be 1 or 60")
	}
	if policy.Burst < 0 || policy.Burst > 1_000_000 {
		return errors.New("burst must be between 0 and 1000000")
	}
	if policy.RejectStatus == 0 {
		policy.RejectStatus = 429
	}
	if policy.RejectStatus < 400 || policy.RejectStatus > 599 {
		return errors.New("rejectStatus must be a 4xx or 5xx status")
	}
	return nil
}

func (policy *WAFPolicy) Validate() error {
	if policy.Mode == "" {
		policy.Mode = "off"
	}
	if !slices.Contains([]string{"off", "detect", "block"}, policy.Mode) {
		return errors.New("mode must be off, detect, or block")
	}
	if policy.Mode != "off" && (policy.ParanoiaLevel < 1 || policy.ParanoiaLevel > 4) {
		return errors.New("paranoiaLevel must be between 1 and 4")
	}
	for _, ruleID := range policy.ExcludedRuleIDs {
		value, err := strconv.Atoi(ruleID)
		if err != nil || value <= 0 {
			return fmt.Errorf("invalid excluded rule id %q", ruleID)
		}
	}
	return nil
}

func (policy *GeoPolicy) Validate() error {
	if policy.Mode == "" {
		policy.Mode = "off"
	}
	if !slices.Contains([]string{"off", "allow", "block"}, policy.Mode) {
		return errors.New("mode must be off, allow, or block")
	}
	for index, country := range policy.Countries {
		country = strings.ToUpper(strings.TrimSpace(country))
		if !countryPattern.MatchString(country) {
			return fmt.Errorf("invalid ISO country code %q", country)
		}
		policy.Countries[index] = country
	}
	slices.Sort(policy.Countries)
	policy.Countries = slices.Compact(policy.Countries)
	if policy.Mode != "off" && len(policy.Countries) == 0 {
		return errors.New("country policy requires at least one country")
	}
	return nil
}

func (policy AccessPolicy) Validate() error {
	for _, cidr := range append(slices.Clone(policy.AllowedCIDRs), policy.DeniedCIDRs...) {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid CIDR %q", cidr)
		}
	}
	return nil
}
