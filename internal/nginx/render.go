package nginx

import (
	"bytes"
	_ "embed"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"text/template"

	"github.com/afdaan/proxy-manager/internal/model"
)

type Renderer struct {
	ACMEWebroot string
}

type renderData struct {
	Revision    string
	ACMEWebroot string
	Sites       []siteData
}

type siteData struct {
	model.Site
	UpstreamName string
	UpstreamURL  string
	UpstreamTLS  bool
	TLSActive    bool
	Rate         string
	Certificate  string
	PrivateKey   string
	GeoDefault   int
	GeoEntries   []geoEntry
}

type geoEntry struct {
	Country string
	Denied  int
}

func (renderer Renderer) Render(config model.GatewayConfig) ([]byte, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if renderer.ACMEWebroot == "" {
		renderer.ACMEWebroot = "/var/lib/proxy-manager/acme"
	}

	data := renderData{Revision: config.Revision, ACMEWebroot: renderer.ACMEWebroot}
	for _, site := range config.Sites {
		if !site.Enabled {
			continue
		}
		prepared, err := renderer.prepareSite(site)
		if err != nil {
			return nil, fmt.Errorf("prepare site %q: %w", site.ID, err)
		}
		data.Sites = append(data.Sites, prepared)
	}

	var output bytes.Buffer
	if err := configTemplate.Execute(&output, data); err != nil {
		return nil, fmt.Errorf("render nginx config: %w", err)
	}
	return output.Bytes(), nil
}

func (renderer Renderer) prepareSite(site model.Site) (siteData, error) {
	parsed, err := url.Parse(site.Upstreams[0].URL)
	if err != nil {
		return siteData{}, err
	}
	prepared := siteData{
		Site:         site,
		UpstreamName: "pm_" + site.ID,
		UpstreamURL:  parsed.Scheme + "://pm_" + site.ID,
		UpstreamTLS:  parsed.Scheme == "https",
	}
	if site.RateLimit.Enabled {
		period := "r/s"
		if site.RateLimit.PeriodSeconds == 60 {
			period = "r/m"
		}
		prepared.Rate = strconv.Itoa(site.RateLimit.Requests) + period
	}
	if site.Geo.Mode == "allow" {
		prepared.GeoDefault = 1
		for _, country := range site.Geo.Countries {
			prepared.GeoEntries = append(prepared.GeoEntries, geoEntry{Country: country})
		}
	} else if site.Geo.Mode == "block" {
		for _, country := range site.Geo.Countries {
			prepared.GeoEntries = append(prepared.GeoEntries, geoEntry{Country: country, Denied: 1})
		}
	}
	if site.TLS.Mode == "automatic" {
		prepared.Certificate = "/etc/letsencrypt/live/" + site.Domains[0] + "/fullchain.pem"
		prepared.PrivateKey = "/etc/letsencrypt/live/" + site.Domains[0] + "/privkey.pem"
		prepared.TLSActive = site.TLS.Ready
	} else if site.TLS.Mode == "custom" {
		prepared.Certificate = site.TLS.CertificatePath
		prepared.PrivateKey = site.TLS.PrivateKeyPath
		prepared.TLSActive = true
	}
	return prepared, nil
}

//go:embed config.tmpl
var configTemplateSource string

var configTemplate = template.Must(template.New("nginx").Funcs(template.FuncMap{
	"join": strings.Join,
	"upstreamAddress": func(rawURL string) string {
		parsed, _ := url.Parse(rawURL)
		host := parsed.Host
		if parsed.Port() == "" {
			port := "80"
			if parsed.Scheme == "https" {
				port = "443"
			}
			if strings.Contains(parsed.Hostname(), ":") {
				host = net.JoinHostPort(parsed.Hostname(), port)
			} else {
				host = parsed.Hostname() + ":" + port
			}
		}
		return host
	},
	"wafFile": func(mode string) string {
		if mode == "detect" {
			return "/etc/proxy-manager/waf/detection-only.conf"
		}
		return "/etc/proxy-manager/waf/blocking.conf"
	},
}).Parse(configTemplateSource))
