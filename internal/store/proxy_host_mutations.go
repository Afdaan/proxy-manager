package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/afdaan/proxy-manager/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ProxyHostDetail struct {
	ID              string                `json:"id"`
	ApplicationID   string                `json:"applicationId"`
	Application     string                `json:"application"`
	Name            string                `json:"name"`
	Enabled         bool                  `json:"enabled"`
	PrimaryDomain   string                `json:"primaryDomain"`
	DomainAliases   []string              `json:"domainAliases"`
	Upstreams       []model.Upstream      `json:"upstreams"`
	LoadBalancing   string                `json:"loadBalancing"`
	TLS             model.TLSPolicy       `json:"tls"`
	RateLimit       model.RateLimitPolicy `json:"rateLimit"`
	WAF             model.WAFPolicy       `json:"waf"`
	Geo             model.GeoPolicy       `json:"geo"`
	Access          model.AccessPolicy    `json:"access"`
	SecurityHeaders bool                  `json:"securityHeaders"`
	MaxBodySizeMB   int                   `json:"maxBodySizeMb"`
	Version         int64                 `json:"version"`
}

type UpdateProxyHostParams struct {
	ID              string
	Name            string
	Enabled         bool
	PrimaryDomain   string
	DomainAliases   []string
	Upstreams       []model.Upstream
	LoadBalancing   string
	RateLimit       model.RateLimitPolicy
	WAF             model.WAFPolicy
	SecurityHeaders bool
	MaxBodySizeMB   int
	Version         int64
	RequestID       string
	SourceIP        string
}

type DeleteProxyHostParams struct {
	ID        string
	Version   int64
	RequestID string
	SourceIP  string
}

type DeleteProxyHostResult struct {
	Deployment Deployment
	Config     model.GatewayConfig
}

func (store *Store) GetProxyHost(ctx context.Context, userID, proxyHostID string) (ProxyHostDetail, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return ProxyHostDetail{}, fmt.Errorf("begin proxy host read: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck
	detail, err := loadProxyHostDetail(ctx, transaction, proxyHostID, false)
	if err != nil {
		return ProxyHostDetail{}, err
	}
	allowed, err := hasApplicationPermission(ctx, transaction, userID, detail.ApplicationID, "application.view")
	if err != nil {
		return ProxyHostDetail{}, err
	}
	if !allowed {
		return ProxyHostDetail{}, ErrForbidden
	}
	if err := transaction.Commit(ctx); err != nil {
		return ProxyHostDetail{}, fmt.Errorf("commit proxy host read: %w", err)
	}
	return detail, nil
}

func (store *Store) UpdateProxyHost(ctx context.Context, userID string, params UpdateProxyHostParams) (CreateProxyHostResult, error) {
	if params.Version < 1 {
		return CreateProxyHostResult{}, errors.New("version must be at least 1")
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CreateProxyHostResult{}, fmt.Errorf("begin proxy host update: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck
	if err := lockGatewayMutation(ctx, transaction); err != nil {
		return CreateProxyHostResult{}, err
	}
	current, err := loadProxyHostDetail(ctx, transaction, params.ID, true)
	if err != nil {
		return CreateProxyHostResult{}, err
	}
	allowed, err := hasApplicationPermission(ctx, transaction, userID, current.ApplicationID, "proxy_hosts.write")
	if err != nil {
		return CreateProxyHostResult{}, err
	}
	if !allowed {
		return CreateProxyHostResult{}, ErrForbidden
	}
	if current.Version != params.Version {
		return CreateProxyHostResult{}, ErrStaleVersion
	}

	site := model.Site{
		ID:              "proxy-host",
		Name:            strings.TrimSpace(params.Name),
		Enabled:         params.Enabled,
		Domains:         append([]string{params.PrimaryDomain}, params.DomainAliases...),
		Upstreams:       params.Upstreams,
		LoadBalancing:   params.LoadBalancing,
		TLS:             current.TLS,
		RateLimit:       params.RateLimit,
		WAF:             params.WAF,
		Geo:             current.Geo,
		Access:          current.Access,
		SecurityHeaders: params.SecurityHeaders,
		MaxBodySizeMB:   params.MaxBodySizeMB,
	}
	if err := site.Validate(); err != nil {
		return CreateProxyHostResult{}, err
	}

	rateLimitPolicy, _ := json.Marshal(site.RateLimit)
	wafPolicy, _ := json.Marshal(site.WAF)
	command, err := transaction.Exec(ctx, `
		UPDATE proxy_hosts
		SET name = $2,
		    enabled = $3,
		    primary_domain = $4,
		    rate_limit_policy = $5,
		    waf_policy = $6,
		    load_balancing = $7,
		    security_headers = $8,
		    max_body_size_mb = $9,
		    version = version + 1,
		    updated_at = now()
		WHERE id = $1 AND version = $10`,
		params.ID,
		site.Name,
		site.Enabled,
		site.Domains[0],
		rateLimitPolicy,
		wafPolicy,
		site.LoadBalancing,
		site.SecurityHeaders,
		site.MaxBodySizeMB,
		params.Version,
	)
	if err != nil {
		if isProxyHostUniqueViolation(err) {
			return CreateProxyHostResult{}, ErrConflict
		}
		return CreateProxyHostResult{}, fmt.Errorf("update proxy host: %w", err)
	}
	if command.RowsAffected() == 0 {
		return CreateProxyHostResult{}, ErrStaleVersion
	}
	if err := replaceProxyHostDomains(ctx, transaction, params.ID, site.Domains); err != nil {
		return CreateProxyHostResult{}, err
	}
	if err := replaceProxyHostUpstreams(ctx, transaction, params.ID, site.Upstreams); err != nil {
		return CreateProxyHostResult{}, err
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_logs (
		    actor_user_id, application_id, action, resource_type, resource_id, request_id, source_ip, details
		) VALUES ($1, $2, 'proxy_host.update', 'proxy_host', $3, $4, NULLIF($5, '')::inet, $6)`,
		userID,
		current.ApplicationID,
		params.ID,
		params.RequestID,
		params.SourceIP,
		map[string]any{"domain": site.Domains[0], "enabled": site.Enabled, "upstreams": len(site.Upstreams)},
	); err != nil {
		return CreateProxyHostResult{}, fmt.Errorf("write proxy host update audit log: %w", err)
	}

	deployment, config, err := createDeploymentRecord(ctx, transaction, userID, CreateDeploymentParams{
		RequestID: params.RequestID,
		SourceIP:  params.SourceIP,
	})
	if err != nil {
		return CreateProxyHostResult{}, err
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE proxy_hosts SET desired_generation = $2 WHERE id = $1`, params.ID, deployment.Generation); err != nil {
		return CreateProxyHostResult{}, fmt.Errorf("record proxy host generation: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return CreateProxyHostResult{}, fmt.Errorf("commit proxy host update: %w", err)
	}
	return CreateProxyHostResult{
		ProxyHost: ProxyHost{
			ID:                  params.ID,
			ApplicationID:       current.ApplicationID,
			Application:         current.Application,
			Name:                site.Name,
			Enabled:             site.Enabled,
			PrimaryDomain:       site.Domains[0],
			UpstreamURL:         firstUpstreamURL(site.Upstreams),
			UpstreamCount:       len(site.Upstreams),
			LoadBalancing:       site.LoadBalancing,
			TLSMode:             site.TLS.Mode,
			WAFMode:             site.WAF.Mode,
			RateLimitEnabled:    site.RateLimit.Enabled,
			CanWrite:            true,
			ConfigurationStatus: "applying",
			Version:             params.Version + 1,
		},
		Deployment: deployment,
		Config:     config,
	}, nil
}

func (store *Store) DeleteProxyHost(ctx context.Context, userID string, params DeleteProxyHostParams) (DeleteProxyHostResult, error) {
	if params.Version < 1 {
		return DeleteProxyHostResult{}, errors.New("version must be at least 1")
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DeleteProxyHostResult{}, fmt.Errorf("begin proxy host delete: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck
	if err := lockGatewayMutation(ctx, transaction); err != nil {
		return DeleteProxyHostResult{}, err
	}
	current, err := loadProxyHostDetail(ctx, transaction, params.ID, true)
	if err != nil {
		return DeleteProxyHostResult{}, err
	}
	allowed, err := hasApplicationPermission(ctx, transaction, userID, current.ApplicationID, "proxy_hosts.write")
	if err != nil {
		return DeleteProxyHostResult{}, err
	}
	if !allowed {
		return DeleteProxyHostResult{}, ErrForbidden
	}
	if current.Version != params.Version {
		return DeleteProxyHostResult{}, ErrStaleVersion
	}
	command, err := transaction.Exec(ctx, "DELETE FROM proxy_hosts WHERE id = $1 AND version = $2", params.ID, params.Version)
	if err != nil {
		return DeleteProxyHostResult{}, fmt.Errorf("delete proxy host: %w", err)
	}
	if command.RowsAffected() == 0 {
		return DeleteProxyHostResult{}, ErrStaleVersion
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_logs (
		    actor_user_id, application_id, action, resource_type, resource_id, request_id, source_ip, details
		) VALUES ($1, $2, 'proxy_host.delete', 'proxy_host', $3, $4, NULLIF($5, '')::inet, $6)`,
		userID,
		current.ApplicationID,
		params.ID,
		params.RequestID,
		params.SourceIP,
		map[string]any{"domain": current.PrimaryDomain, "name": current.Name},
	); err != nil {
		return DeleteProxyHostResult{}, fmt.Errorf("write proxy host delete audit log: %w", err)
	}
	deployment, config, err := createDeploymentRecord(ctx, transaction, userID, CreateDeploymentParams{
		RequestID: params.RequestID,
		SourceIP:  params.SourceIP,
	})
	if err != nil {
		return DeleteProxyHostResult{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return DeleteProxyHostResult{}, fmt.Errorf("commit proxy host delete: %w", err)
	}
	return DeleteProxyHostResult{Deployment: deployment, Config: config}, nil
}

func loadProxyHostDetail(ctx context.Context, transaction pgx.Tx, proxyHostID string, forUpdate bool) (ProxyHostDetail, error) {
	query := `
		SELECT proxy_hosts.id,
		       proxy_hosts.application_id,
		       applications.name,
		       proxy_hosts.name,
		       proxy_hosts.enabled,
		       proxy_hosts.primary_domain,
		       proxy_hosts.load_balancing,
		       proxy_hosts.tls_policy,
		       proxy_hosts.rate_limit_policy,
		       proxy_hosts.waf_policy,
		       proxy_hosts.geo_policy,
		       proxy_hosts.access_policy,
		       proxy_hosts.security_headers,
		       proxy_hosts.max_body_size_mb,
		       proxy_hosts.version
		FROM proxy_hosts
		JOIN applications ON applications.id = proxy_hosts.application_id
		WHERE proxy_hosts.id = $1`
	if forUpdate {
		query += " FOR UPDATE OF proxy_hosts"
	}
	var detail ProxyHostDetail
	var tlsPolicy, rateLimitPolicy, wafPolicy, geoPolicy, accessPolicy []byte
	err := transaction.QueryRow(ctx, query, proxyHostID).Scan(
		&detail.ID,
		&detail.ApplicationID,
		&detail.Application,
		&detail.Name,
		&detail.Enabled,
		&detail.PrimaryDomain,
		&detail.LoadBalancing,
		&tlsPolicy,
		&rateLimitPolicy,
		&wafPolicy,
		&geoPolicy,
		&accessPolicy,
		&detail.SecurityHeaders,
		&detail.MaxBodySizeMB,
		&detail.Version,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProxyHostDetail{}, ErrNotFound
	}
	if err != nil {
		return ProxyHostDetail{}, fmt.Errorf("load proxy host: %w", err)
	}
	site := model.Site{}
	if err := decodePolicies(&site, tlsPolicy, rateLimitPolicy, wafPolicy, geoPolicy, accessPolicy); err != nil {
		return ProxyHostDetail{}, fmt.Errorf("decode proxy host policies: %w", err)
	}
	detail.TLS = site.TLS
	detail.RateLimit = site.RateLimit
	detail.WAF = site.WAF
	detail.Geo = site.Geo
	detail.Access = site.Access

	rows, err := transaction.Query(ctx, `
		SELECT domain
		FROM proxy_host_domains
		WHERE proxy_host_id = $1 AND domain <> $2
		ORDER BY domain`, proxyHostID, detail.PrimaryDomain)
	if err != nil {
		return ProxyHostDetail{}, fmt.Errorf("list proxy host aliases: %w", err)
	}
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			rows.Close()
			return ProxyHostDetail{}, fmt.Errorf("scan proxy host alias: %w", err)
		}
		detail.DomainAliases = append(detail.DomainAliases, domain)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ProxyHostDetail{}, fmt.Errorf("iterate proxy host aliases: %w", err)
	}
	rows.Close()

	upstreamRows, err := transaction.Query(ctx, `
		SELECT url, weight, max_fails, fail_after_seconds, backup
		FROM upstreams
		WHERE proxy_host_id = $1
		ORDER BY created_at, id`, proxyHostID)
	if err != nil {
		return ProxyHostDetail{}, fmt.Errorf("list proxy host upstreams: %w", err)
	}
	defer upstreamRows.Close()
	for upstreamRows.Next() {
		var upstream model.Upstream
		if err := upstreamRows.Scan(&upstream.URL, &upstream.Weight, &upstream.MaxFails, &upstream.FailAfter, &upstream.Backup); err != nil {
			return ProxyHostDetail{}, fmt.Errorf("scan proxy host upstream: %w", err)
		}
		detail.Upstreams = append(detail.Upstreams, upstream)
	}
	if err := upstreamRows.Err(); err != nil {
		return ProxyHostDetail{}, fmt.Errorf("iterate proxy host upstreams: %w", err)
	}
	return detail, nil
}

func replaceProxyHostDomains(ctx context.Context, transaction pgx.Tx, proxyHostID string, domains []string) error {
	if _, err := transaction.Exec(ctx, "DELETE FROM proxy_host_domains WHERE proxy_host_id = $1", proxyHostID); err != nil {
		return fmt.Errorf("clear proxy host domains: %w", err)
	}
	for _, domain := range domains {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO proxy_host_domains (proxy_host_id, domain) VALUES ($1, $2)`, proxyHostID, domain); err != nil {
			if isProxyHostUniqueViolation(err) {
				return ErrConflict
			}
			return fmt.Errorf("insert proxy host domain: %w", err)
		}
	}
	return nil
}

func replaceProxyHostUpstreams(ctx context.Context, transaction pgx.Tx, proxyHostID string, upstreams []model.Upstream) error {
	if _, err := transaction.Exec(ctx, "DELETE FROM upstreams WHERE proxy_host_id = $1", proxyHostID); err != nil {
		return fmt.Errorf("clear proxy host upstreams: %w", err)
	}
	for _, upstream := range upstreams {
		upstreamID, err := randomUUID()
		if err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `
			INSERT INTO upstreams (id, proxy_host_id, url, weight, max_fails, fail_after_seconds, backup)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			upstreamID, proxyHostID, upstream.URL, upstream.Weight, upstream.MaxFails, upstream.FailAfter, upstream.Backup); err != nil {
			return fmt.Errorf("insert proxy host upstream: %w", err)
		}
	}
	return nil
}

func isProxyHostUniqueViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}

func firstUpstreamURL(upstreams []model.Upstream) string {
	if len(upstreams) == 0 {
		return ""
	}
	return upstreams[0].URL
}
