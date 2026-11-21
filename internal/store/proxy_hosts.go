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

type Application struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	CanWrite bool   `json:"canWrite"`
}

type ProxyHost struct {
	ID               string `json:"id"`
	ApplicationID    string `json:"applicationId"`
	Application      string `json:"application"`
	Name             string `json:"name"`
	Enabled          bool   `json:"enabled"`
	PrimaryDomain    string `json:"primaryDomain"`
	UpstreamURL      string `json:"upstreamUrl"`
	UpstreamCount    int    `json:"upstreamCount"`
	LoadBalancing    string `json:"loadBalancing"`
	TLSMode          string `json:"tlsMode"`
	WAFMode          string `json:"wafMode"`
	RateLimitEnabled bool   `json:"rateLimitEnabled"`
	Version          int64  `json:"version"`
}

type CreateProxyHostParams struct {
	ApplicationID   string
	Name            string
	PrimaryDomain   string
	DomainAliases   []string
	Upstreams       []model.Upstream
	LoadBalancing   string
	RateLimit       model.RateLimitPolicy
	WAF             model.WAFPolicy
	SecurityHeaders bool
	MaxBodySizeMB   int
	RequestID       string
	SourceIP        string
}

func (store *Store) ListApplications(ctx context.Context, userID string) ([]Application, error) {
	rows, err := store.Pool.Query(ctx, `
		SELECT applications.id, applications.name, applications.slug,
		       EXISTS (
		           SELECT 1
		           FROM application_memberships memberships
		           JOIN role_permissions ON role_permissions.role_id = memberships.role_id
		           WHERE memberships.application_id = applications.id
		             AND memberships.user_id = $1
		             AND role_permissions.permission_name = 'proxy_hosts.write'
		       ) OR EXISTS (
		           SELECT 1
		           FROM user_platform_roles platform_roles
		           JOIN role_permissions ON role_permissions.role_id = platform_roles.role_id
		           WHERE platform_roles.user_id = $1
		             AND role_permissions.permission_name = 'platform.admin'
		       ) AS can_write
		FROM applications
		WHERE EXISTS (
		    SELECT 1 FROM application_memberships
		    WHERE application_id = applications.id AND user_id = $1
		) OR EXISTS (
		    SELECT 1
		    FROM user_platform_roles
		    JOIN role_permissions ON role_permissions.role_id = user_platform_roles.role_id
		    WHERE user_platform_roles.user_id = $1
		      AND role_permissions.permission_name = 'platform.admin'
		)
		ORDER BY applications.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	defer rows.Close()

	applications := make([]Application, 0)
	for rows.Next() {
		var application Application
		if err := rows.Scan(&application.ID, &application.Name, &application.Slug, &application.CanWrite); err != nil {
			return nil, fmt.Errorf("scan application: %w", err)
		}
		applications = append(applications, application)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applications: %w", err)
	}
	return applications, nil
}

func (store *Store) ListProxyHosts(ctx context.Context, userID string) ([]ProxyHost, error) {
	rows, err := store.Pool.Query(ctx, `
		SELECT proxy_hosts.id,
		       proxy_hosts.application_id,
		       applications.name,
		       proxy_hosts.name,
		       proxy_hosts.enabled,
		       proxy_hosts.primary_domain,
		       upstreams.url,
		       upstreams.upstream_count,
		       proxy_hosts.load_balancing,
		       COALESCE(proxy_hosts.tls_policy->>'mode', 'off'),
		       COALESCE(proxy_hosts.waf_policy->>'mode', 'off'),
		       COALESCE((proxy_hosts.rate_limit_policy->>'enabled')::boolean, false),
		       proxy_hosts.version
		FROM proxy_hosts
		JOIN applications ON applications.id = proxy_hosts.application_id
		JOIN LATERAL (
		    SELECT url, COUNT(*) OVER () AS upstream_count FROM upstreams
		    WHERE proxy_host_id = proxy_hosts.id
		    ORDER BY created_at, id
		    LIMIT 1
		) upstreams ON true
		WHERE EXISTS (
		    SELECT 1 FROM application_memberships
		    WHERE application_id = proxy_hosts.application_id AND user_id = $1
		) OR EXISTS (
		    SELECT 1
		    FROM user_platform_roles
		    JOIN role_permissions ON role_permissions.role_id = user_platform_roles.role_id
		    WHERE user_platform_roles.user_id = $1
		      AND role_permissions.permission_name = 'platform.admin'
		)
		ORDER BY proxy_hosts.primary_domain`, userID)
	if err != nil {
		return nil, fmt.Errorf("list proxy hosts: %w", err)
	}
	defer rows.Close()

	hosts := make([]ProxyHost, 0)
	for rows.Next() {
		var host ProxyHost
		if err := rows.Scan(
			&host.ID,
			&host.ApplicationID,
			&host.Application,
			&host.Name,
			&host.Enabled,
			&host.PrimaryDomain,
			&host.UpstreamURL,
			&host.UpstreamCount,
			&host.LoadBalancing,
			&host.TLSMode,
			&host.WAFMode,
			&host.RateLimitEnabled,
			&host.Version,
		); err != nil {
			return nil, fmt.Errorf("scan proxy host: %w", err)
		}
		hosts = append(hosts, host)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxy hosts: %w", err)
	}
	return hosts, nil
}

func (store *Store) CreateProxyHost(ctx context.Context, userID string, params CreateProxyHostParams) (ProxyHost, error) {
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" || len(params.Name) > 120 {
		return ProxyHost{}, errors.New("name must contain 1 to 120 characters")
	}
	if len(params.Upstreams) == 0 {
		return ProxyHost{}, errors.New("at least one upstream is required")
	}
	site := model.Site{
		ID:              "proxy-host",
		Name:            params.Name,
		Enabled:         true,
		Domains:         append([]string{params.PrimaryDomain}, params.DomainAliases...),
		Upstreams:       params.Upstreams,
		LoadBalancing:   params.LoadBalancing,
		RateLimit:       params.RateLimit,
		WAF:             params.WAF,
		SecurityHeaders: params.SecurityHeaders,
		MaxBodySizeMB:   params.MaxBodySizeMB,
	}
	if err := site.Validate(); err != nil {
		return ProxyHost{}, err
	}
	domain := site.Domains[0]
	hostID, err := randomUUID()
	if err != nil {
		return ProxyHost{}, err
	}

	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ProxyHost{}, fmt.Errorf("begin proxy host create: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	allowed, err := hasApplicationPermission(ctx, transaction, userID, params.ApplicationID, "proxy_hosts.write")
	if err != nil {
		return ProxyHost{}, err
	}
	if !allowed {
		return ProxyHost{}, ErrForbidden
	}

	tlsPolicy, _ := json.Marshal(model.TLSPolicy{Mode: "off", RedirectHTTP: true})
	rateLimitPolicy, _ := json.Marshal(site.RateLimit)
	wafPolicy, _ := json.Marshal(site.WAF)
	geoPolicy, _ := json.Marshal(model.GeoPolicy{Mode: "off"})
	accessPolicy, _ := json.Marshal(model.AccessPolicy{})

	_, err = transaction.Exec(ctx, `
		INSERT INTO proxy_hosts (
		    id, application_id, name, enabled, primary_domain,
		    tls_policy, rate_limit_policy, waf_policy, geo_policy, access_policy,
		    load_balancing, security_headers, max_body_size_mb
		) VALUES ($1, $2, $3, true, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		hostID,
		params.ApplicationID,
		params.Name,
		domain,
		tlsPolicy,
		rateLimitPolicy,
		wafPolicy,
		geoPolicy,
		accessPolicy,
		site.LoadBalancing,
		site.SecurityHeaders,
		site.MaxBodySizeMB,
	)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return ProxyHost{}, ErrConflict
		}
		return ProxyHost{}, fmt.Errorf("insert proxy host: %w", err)
	}
	for _, hostDomain := range site.Domains {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO proxy_host_domains (proxy_host_id, domain) VALUES ($1, $2)`, hostID, hostDomain); err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) && databaseError.Code == "23505" {
				return ProxyHost{}, ErrConflict
			}
			return ProxyHost{}, fmt.Errorf("insert proxy host domain: %w", err)
		}
	}
	for _, upstream := range site.Upstreams {
		upstreamID, err := randomUUID()
		if err != nil {
			return ProxyHost{}, err
		}
		if _, err := transaction.Exec(ctx, `
			INSERT INTO upstreams (id, proxy_host_id, url, weight, max_fails, fail_after_seconds, backup)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, upstreamID, hostID, upstream.URL, upstream.Weight, upstream.MaxFails, upstream.FailAfter, upstream.Backup); err != nil {
			return ProxyHost{}, fmt.Errorf("insert upstream: %w", err)
		}
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_logs (
		    actor_user_id, application_id, action, resource_type, resource_id, request_id, source_ip, details
		) VALUES ($1, $2, 'proxy_host.create', 'proxy_host', $3, $4, NULLIF($5, '')::inet, $6)`,
		userID,
		params.ApplicationID,
		hostID,
		params.RequestID,
		params.SourceIP,
		map[string]any{"domain": domain, "upstreams": len(site.Upstreams)},
	); err != nil {
		return ProxyHost{}, fmt.Errorf("write proxy host audit log: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return ProxyHost{}, fmt.Errorf("commit proxy host create: %w", err)
	}

	var applicationName string
	if err := store.Pool.QueryRow(ctx, "SELECT name FROM applications WHERE id = $1", params.ApplicationID).Scan(&applicationName); err != nil {
		return ProxyHost{}, fmt.Errorf("read created proxy host application: %w", err)
	}
	return ProxyHost{
		ID:               hostID,
		ApplicationID:    params.ApplicationID,
		Application:      applicationName,
		Name:             params.Name,
		Enabled:          true,
		PrimaryDomain:    domain,
		UpstreamURL:      site.Upstreams[0].URL,
		UpstreamCount:    len(site.Upstreams),
		LoadBalancing:    site.LoadBalancing,
		TLSMode:          "off",
		WAFMode:          site.WAF.Mode,
		RateLimitEnabled: site.RateLimit.Enabled,
		Version:          1,
	}, nil
}

func hasApplicationPermission(ctx context.Context, transaction pgx.Tx, userID, applicationID, permission string) (bool, error) {
	var allowed bool
	err := transaction.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1
		    FROM user_platform_roles
		    JOIN role_permissions ON role_permissions.role_id = user_platform_roles.role_id
		    WHERE user_platform_roles.user_id = $1
		      AND role_permissions.permission_name = 'platform.admin'
		) OR EXISTS (
		    SELECT 1
		    FROM application_memberships
		    JOIN role_permissions ON role_permissions.role_id = application_memberships.role_id
		    WHERE application_memberships.user_id = $1
		      AND application_memberships.application_id = $2
		      AND role_permissions.permission_name = $3
		)`, userID, applicationID, permission).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check application permission: %w", err)
	}
	return allowed, nil
}

type Overview struct {
	ProxyHosts   int `json:"proxyHosts"`
	Certificates int `json:"certificates"`
	BlockedToday int `json:"blockedToday"`
}

func (store *Store) Overview(ctx context.Context, userID string) (Overview, error) {
	var overview Overview
	err := store.Pool.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE COALESCE(proxy_hosts.tls_policy->>'mode', 'off') <> 'off')
		FROM proxy_hosts
		WHERE EXISTS (
		    SELECT 1 FROM application_memberships
		    WHERE application_id = proxy_hosts.application_id AND user_id = $1
		) OR EXISTS (
		    SELECT 1
		    FROM user_platform_roles
		    JOIN role_permissions ON role_permissions.role_id = user_platform_roles.role_id
		    WHERE user_platform_roles.user_id = $1
		      AND role_permissions.permission_name = 'platform.admin'
		)`, userID).Scan(&overview.ProxyHosts, &overview.Certificates)
	if err != nil {
		return Overview{}, fmt.Errorf("read overview: %w", err)
	}
	return overview, nil
}
