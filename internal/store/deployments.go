package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/afdaan/proxy-manager/internal/model"
	"github.com/jackc/pgx/v5"
)

type Deployment struct {
	ID           string     `json:"id"`
	Generation   uint64     `json:"generation"`
	Revision     string     `json:"revision"`
	Status       string     `json:"status"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
}

type PreparedDeployment struct {
	Deployment Deployment
	Config     model.GatewayConfig
	Process    bool
}

type CreateDeploymentParams struct {
	RequestID string
	SourceIP  string
}

func (store *Store) CreateDeployment(ctx context.Context, userID string, params CreateDeploymentParams) (Deployment, model.GatewayConfig, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Deployment{}, model.GatewayConfig{}, fmt.Errorf("begin deployment: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	if err := lockGatewayMutation(ctx, transaction); err != nil {
		return Deployment{}, model.GatewayConfig{}, err
	}
	allowed, err := hasDeploymentPermission(ctx, transaction, userID)
	if err != nil {
		return Deployment{}, model.GatewayConfig{}, err
	}
	if !allowed {
		return Deployment{}, model.GatewayConfig{}, ErrForbidden
	}
	deployment, config, err := createDeploymentRecord(ctx, transaction, userID, params)
	if err != nil {
		return Deployment{}, model.GatewayConfig{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return Deployment{}, model.GatewayConfig{}, fmt.Errorf("commit deployment: %w", err)
	}
	return deployment, config, nil
}

func createDeploymentRecord(ctx context.Context, transaction pgx.Tx, userID string, params CreateDeploymentParams) (Deployment, model.GatewayConfig, error) {
	deploymentID, err := randomUUID()
	if err != nil {
		return Deployment{}, model.GatewayConfig{}, err
	}
	revisionID, err := randomUUID()
	if err != nil {
		return Deployment{}, model.GatewayConfig{}, err
	}

	var generation uint64
	if err := transaction.QueryRow(ctx, "SELECT COALESCE(MAX(generation), 0) + 1 FROM config_revisions").Scan(&generation); err != nil {
		return Deployment{}, model.GatewayConfig{}, fmt.Errorf("allocate deployment generation: %w", err)
	}
	now := time.Now().UTC()
	revision := fmt.Sprintf("%s--rev-%d--%s", now.Format("2006-01-02_15-04-05Z"), generation, shortIdentifier(revisionID))
	config, err := loadGatewayConfig(ctx, transaction, generation, revision)
	if err != nil {
		return Deployment{}, model.GatewayConfig{}, err
	}
	desiredState, err := json.Marshal(config)
	if err != nil {
		return Deployment{}, model.GatewayConfig{}, fmt.Errorf("encode desired state: %w", err)
	}

	if _, err := transaction.Exec(ctx, `
		INSERT INTO config_revisions (id, generation, revision, desired_state, created_by)
		VALUES ($1, $2, $3, $4, $5)`, revisionID, generation, revision, desiredState, userID); err != nil {
		return Deployment{}, model.GatewayConfig{}, fmt.Errorf("create config revision: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO deployment_runs (id, config_revision_id, status)
		VALUES ($1, $2, 'queued')`, deploymentID, revisionID); err != nil {
		return Deployment{}, model.GatewayConfig{}, fmt.Errorf("create deployment run: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_logs (
		    actor_user_id, action, resource_type, resource_id, request_id, source_ip, details
		) VALUES ($1, 'deployment.create', 'deployment', $2, $3, NULLIF($4, '')::inet, $5)`,
		userID,
		deploymentID,
		params.RequestID,
		params.SourceIP,
		map[string]any{"generation": generation, "revision": revision, "proxyHosts": len(config.Sites)},
	); err != nil {
		return Deployment{}, model.GatewayConfig{}, fmt.Errorf("write deployment audit log: %w", err)
	}
	return Deployment{
		ID:         deploymentID,
		Generation: generation,
		Revision:   revision,
		Status:     "queued",
		CreatedAt:  now,
	}, config, nil
}

func lockGatewayMutation(ctx context.Context, transaction pgx.Tx) error {
	if _, err := transaction.Exec(ctx, "SELECT pg_advisory_xact_lock(742198533)"); err != nil {
		return fmt.Errorf("lock gateway mutation: %w", err)
	}
	return nil
}

func (store *Store) TransitionDeployment(ctx context.Context, deploymentID, expectedStatus, nextStatus, errorMessage string) (Deployment, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return Deployment{}, fmt.Errorf("begin deployment transition: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	deployment, err := transitionDeploymentTx(ctx, transaction, deploymentID, expectedStatus, nextStatus, errorMessage)
	if err != nil {
		return Deployment{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("commit deployment transition: %w", err)
	}
	return deployment, nil
}

func (store *Store) PrepareDeployment(ctx context.Context, deploymentID string) (PreparedDeployment, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return PreparedDeployment{}, fmt.Errorf("begin deployment preparation: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	var prepared PreparedDeployment
	var desiredState []byte
	err = transaction.QueryRow(ctx, `
		SELECT deployment_runs.id,
		       config_revisions.generation,
		       config_revisions.revision,
		       deployment_runs.status,
		       COALESCE(deployment_runs.error_message, ''),
		       deployment_runs.started_at,
		       deployment_runs.finished_at,
		       deployment_runs.created_at,
		       config_revisions.desired_state
		FROM deployment_runs
		JOIN config_revisions ON config_revisions.id = deployment_runs.config_revision_id
		WHERE deployment_runs.id = $1
		FOR UPDATE`, deploymentID).Scan(
		&prepared.Deployment.ID,
		&prepared.Deployment.Generation,
		&prepared.Deployment.Revision,
		&prepared.Deployment.Status,
		&prepared.Deployment.ErrorMessage,
		&prepared.Deployment.StartedAt,
		&prepared.Deployment.FinishedAt,
		&prepared.Deployment.CreatedAt,
		&desiredState,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreparedDeployment{}, ErrNotFound
	}
	if err != nil {
		return PreparedDeployment{}, fmt.Errorf("load deployment: %w", err)
	}
	if deploymentFinished(prepared.Deployment.Status) {
		if err := transaction.Commit(ctx); err != nil {
			return PreparedDeployment{}, fmt.Errorf("commit deployment read: %w", err)
		}
		return prepared, nil
	}

	var newerExists bool
	if err := transaction.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM config_revisions WHERE generation > $1)`,
		prepared.Deployment.Generation,
	).Scan(&newerExists); err != nil {
		return PreparedDeployment{}, fmt.Errorf("check newer deployment: %w", err)
	}
	if newerExists && prepared.Deployment.Status != "applying" {
		prepared.Deployment, err = transitionDeploymentTx(ctx, transaction, deploymentID, prepared.Deployment.Status, "superseded", "newer gateway configuration is available")
		if err != nil {
			return PreparedDeployment{}, err
		}
		if err := transaction.Commit(ctx); err != nil {
			return PreparedDeployment{}, fmt.Errorf("commit superseded deployment: %w", err)
		}
		return prepared, nil
	}

	err = transaction.QueryRow(ctx, `
		UPDATE deployment_runs
		SET status = 'validating',
		    error_message = NULL,
		    attempt_count = attempt_count + 1,
		    started_at = COALESCE(started_at, now()),
		    finished_at = NULL,
		    updated_at = now()
		FROM config_revisions
		WHERE deployment_runs.id = $1
		  AND config_revisions.id = deployment_runs.config_revision_id
		RETURNING deployment_runs.id,
		          config_revisions.generation,
		          config_revisions.revision,
		          deployment_runs.status,
		          COALESCE(deployment_runs.error_message, ''),
		          deployment_runs.started_at,
		          deployment_runs.finished_at,
		          deployment_runs.created_at`, deploymentID).Scan(
		&prepared.Deployment.ID,
		&prepared.Deployment.Generation,
		&prepared.Deployment.Revision,
		&prepared.Deployment.Status,
		&prepared.Deployment.ErrorMessage,
		&prepared.Deployment.StartedAt,
		&prepared.Deployment.FinishedAt,
		&prepared.Deployment.CreatedAt,
	)
	if err != nil {
		return PreparedDeployment{}, fmt.Errorf("claim deployment: %w", err)
	}
	if err := json.Unmarshal(desiredState, &prepared.Config); err != nil {
		return PreparedDeployment{}, fmt.Errorf("decode desired state: %w", err)
	}
	prepared.Process = true
	if err := transaction.Commit(ctx); err != nil {
		return PreparedDeployment{}, fmt.Errorf("commit deployment preparation: %w", err)
	}
	return prepared, nil
}

func (store *Store) NextDeploymentID(ctx context.Context) (string, error) {
	var deploymentID string
	err := store.Pool.QueryRow(ctx, `
		SELECT deployment_runs.id
		FROM deployment_runs
		JOIN config_revisions ON config_revisions.id = deployment_runs.config_revision_id
		WHERE deployment_runs.status IN ('queued', 'validating', 'applying', 'retrying')
		  AND deployment_runs.next_attempt_at <= now()
		ORDER BY config_revisions.generation DESC
		LIMIT 1`).Scan(&deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find pending deployment: %w", err)
	}
	return deploymentID, nil
}

func (store *Store) RetryDeployment(ctx context.Context, deploymentID, expectedStatus, errorMessage string, nextAttempt time.Time) (Deployment, error) {
	var deployment Deployment
	err := store.Pool.QueryRow(ctx, `
		UPDATE deployment_runs
		SET status = 'retrying',
		    error_message = NULLIF($3, ''),
		    next_attempt_at = $4,
		    finished_at = NULL,
		    updated_at = now()
		FROM config_revisions
		WHERE deployment_runs.id = $1
		  AND deployment_runs.status = $2
		  AND config_revisions.id = deployment_runs.config_revision_id
		RETURNING deployment_runs.id,
		          config_revisions.generation,
		          config_revisions.revision,
		          deployment_runs.status,
		          COALESCE(deployment_runs.error_message, ''),
		          deployment_runs.started_at,
		          deployment_runs.finished_at,
		          deployment_runs.created_at`,
		deploymentID, expectedStatus, boundedError(errorMessage), nextAttempt,
	).Scan(
		&deployment.ID,
		&deployment.Generation,
		&deployment.Revision,
		&deployment.Status,
		&deployment.ErrorMessage,
		&deployment.StartedAt,
		&deployment.FinishedAt,
		&deployment.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrConflict
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("schedule deployment retry: %w", err)
	}
	return deployment, nil
}

func (store *Store) CompleteDeployment(ctx context.Context, deploymentID string) (Deployment, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return Deployment{}, fmt.Errorf("begin deployment completion: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck

	deployment, err := transitionDeploymentTx(ctx, transaction, deploymentID, "applying", "succeeded", "")
	if err != nil {
		return Deployment{}, err
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE gateway_state
		SET active_generation = $1, updated_at = now()
		WHERE singleton = true`, deployment.Generation); err != nil {
		return Deployment{}, fmt.Errorf("record active generation: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("commit deployment completion: %w", err)
	}
	return deployment, nil
}

func transitionDeploymentTx(ctx context.Context, transaction pgx.Tx, deploymentID, expectedStatus, nextStatus, errorMessage string) (Deployment, error) {
	var deployment Deployment
	err := transaction.QueryRow(ctx, `
		UPDATE deployment_runs
		SET status = $3,
		    error_message = NULLIF($4, ''),
		    started_at = CASE WHEN $3 IN ('validating', 'applying') THEN COALESCE(started_at, now()) ELSE started_at END,
		    finished_at = CASE WHEN $3 IN ('succeeded', 'failed', 'superseded', 'rolled_back') THEN now() ELSE NULL END,
		    updated_at = now()
		FROM config_revisions
		WHERE deployment_runs.id = $1
		  AND deployment_runs.status = $2
		  AND config_revisions.id = deployment_runs.config_revision_id
		RETURNING deployment_runs.id,
		          config_revisions.generation,
		          config_revisions.revision,
		          deployment_runs.status,
		          COALESCE(deployment_runs.error_message, ''),
		          deployment_runs.started_at,
		          deployment_runs.finished_at,
		          deployment_runs.created_at`,
		deploymentID, expectedStatus, nextStatus, boundedError(errorMessage),
	).Scan(
		&deployment.ID,
		&deployment.Generation,
		&deployment.Revision,
		&deployment.Status,
		&deployment.ErrorMessage,
		&deployment.StartedAt,
		&deployment.FinishedAt,
		&deployment.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrConflict
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("transition deployment: %w", err)
	}
	return deployment, nil
}

func deploymentFinished(status string) bool {
	return status == "succeeded" || status == "failed" || status == "superseded" || status == "rolled_back"
}

func (store *Store) ListDeployments(ctx context.Context, userID string, limit int) ([]Deployment, error) {
	allowed, err := store.canDeploy(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, err := store.Pool.Query(ctx, `
		SELECT deployment_runs.id,
		       config_revisions.generation,
		       config_revisions.revision,
		       deployment_runs.status,
		       COALESCE(deployment_runs.error_message, ''),
		       deployment_runs.started_at,
		       deployment_runs.finished_at,
		       deployment_runs.created_at
		FROM deployment_runs
		JOIN config_revisions ON config_revisions.id = deployment_runs.config_revision_id
		ORDER BY config_revisions.generation DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	deployments := make([]Deployment, 0, limit)
	for rows.Next() {
		var deployment Deployment
		if err := rows.Scan(
			&deployment.ID,
			&deployment.Generation,
			&deployment.Revision,
			&deployment.Status,
			&deployment.ErrorMessage,
			&deployment.StartedAt,
			&deployment.FinishedAt,
			&deployment.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
		}
		deployments = append(deployments, deployment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deployments: %w", err)
	}
	return deployments, nil
}

func loadGatewayConfig(ctx context.Context, transaction pgx.Tx, generation uint64, revision string) (model.GatewayConfig, error) {
	rows, err := transaction.Query(ctx, `
		SELECT id,
		       name,
		       primary_domain,
		       load_balancing,
		       tls_policy,
		       rate_limit_policy,
		       waf_policy,
		       geo_policy,
		       access_policy,
		       security_headers,
		       max_body_size_mb
		FROM proxy_hosts
		WHERE enabled = true
		ORDER BY primary_domain`)
	if err != nil {
		return model.GatewayConfig{}, fmt.Errorf("load proxy hosts for deployment: %w", err)
	}
	defer rows.Close()

	config := model.GatewayConfig{Generation: generation, Revision: revision}
	primaryDomains := make(map[string]string)
	for rows.Next() {
		var site model.Site
		var primaryDomain string
		var tlsPolicy, rateLimitPolicy, wafPolicy, geoPolicy, accessPolicy []byte
		if err := rows.Scan(
			&site.ID,
			&site.Name,
			&primaryDomain,
			&site.LoadBalancing,
			&tlsPolicy,
			&rateLimitPolicy,
			&wafPolicy,
			&geoPolicy,
			&accessPolicy,
			&site.SecurityHeaders,
			&site.MaxBodySizeMB,
		); err != nil {
			return model.GatewayConfig{}, fmt.Errorf("scan deployment proxy host: %w", err)
		}
		site.Enabled = true
		site.Domains = []string{primaryDomain}
		primaryDomains[site.ID] = primaryDomain
		if err := decodePolicies(&site, tlsPolicy, rateLimitPolicy, wafPolicy, geoPolicy, accessPolicy); err != nil {
			return model.GatewayConfig{}, fmt.Errorf("decode policies for %s: %w", primaryDomain, err)
		}
		config.Sites = append(config.Sites, site)
	}
	if err := rows.Err(); err != nil {
		return model.GatewayConfig{}, fmt.Errorf("iterate deployment proxy hosts: %w", err)
	}

	sites := make(map[string]*model.Site, len(config.Sites))
	for index := range config.Sites {
		sites[config.Sites[index].ID] = &config.Sites[index]
	}
	if err := loadDeploymentDomains(ctx, transaction, sites, primaryDomains); err != nil {
		return model.GatewayConfig{}, err
	}
	if err := loadDeploymentUpstreams(ctx, transaction, sites); err != nil {
		return model.GatewayConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return model.GatewayConfig{}, fmt.Errorf("validate desired state: %w", err)
	}
	return config, nil
}

func loadDeploymentDomains(ctx context.Context, transaction pgx.Tx, sites map[string]*model.Site, primaryDomains map[string]string) error {
	rows, err := transaction.Query(ctx, `
		SELECT proxy_host_domains.proxy_host_id, proxy_host_domains.domain
		FROM proxy_host_domains
		JOIN proxy_hosts ON proxy_hosts.id = proxy_host_domains.proxy_host_id
		WHERE proxy_hosts.enabled = true
		ORDER BY proxy_hosts.primary_domain, proxy_host_domains.domain`)
	if err != nil {
		return fmt.Errorf("load deployment domains: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hostID, domain string
		if err := rows.Scan(&hostID, &domain); err != nil {
			return fmt.Errorf("scan deployment domain: %w", err)
		}
		if site := sites[hostID]; site != nil && domain != primaryDomains[hostID] {
			site.Domains = append(site.Domains, domain)
		}
	}
	return rows.Err()
}

func loadDeploymentUpstreams(ctx context.Context, transaction pgx.Tx, sites map[string]*model.Site) error {
	rows, err := transaction.Query(ctx, `
		SELECT upstreams.proxy_host_id,
		       upstreams.url,
		       upstreams.weight,
		       upstreams.max_fails,
		       upstreams.fail_after_seconds,
		       upstreams.backup
		FROM upstreams
		JOIN proxy_hosts ON proxy_hosts.id = upstreams.proxy_host_id
		WHERE proxy_hosts.enabled = true
		ORDER BY proxy_hosts.primary_domain, upstreams.created_at, upstreams.id`)
	if err != nil {
		return fmt.Errorf("load deployment upstreams: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hostID string
		var upstream model.Upstream
		if err := rows.Scan(&hostID, &upstream.URL, &upstream.Weight, &upstream.MaxFails, &upstream.FailAfter, &upstream.Backup); err != nil {
			return fmt.Errorf("scan deployment upstream: %w", err)
		}
		if site := sites[hostID]; site != nil {
			site.Upstreams = append(site.Upstreams, upstream)
		}
	}
	return rows.Err()
}

func decodePolicies(site *model.Site, tlsPolicy, rateLimitPolicy, wafPolicy, geoPolicy, accessPolicy []byte) error {
	for _, item := range []struct {
		name   string
		value  []byte
		target any
	}{
		{name: "TLS", value: tlsPolicy, target: &site.TLS},
		{name: "rate limit", value: rateLimitPolicy, target: &site.RateLimit},
		{name: "WAF", value: wafPolicy, target: &site.WAF},
		{name: "GeoIP", value: geoPolicy, target: &site.Geo},
		{name: "access", value: accessPolicy, target: &site.Access},
	} {
		if err := json.Unmarshal(item.value, item.target); err != nil {
			return fmt.Errorf("%s policy: %w", item.name, err)
		}
	}
	return nil
}

func hasDeploymentPermission(ctx context.Context, transaction pgx.Tx, userID string) (bool, error) {
	var allowed bool
	err := transaction.QueryRow(ctx, deploymentPermissionQuery, userID).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check deployment permission: %w", err)
	}
	return allowed, nil
}

func (store *Store) canDeploy(ctx context.Context, userID string) (bool, error) {
	var allowed bool
	if err := store.Pool.QueryRow(ctx, deploymentPermissionQuery, userID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("check deployment permission: %w", err)
	}
	return allowed, nil
}

const deploymentPermissionQuery = `
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
	      AND role_permissions.permission_name IN ('proxy_hosts.write', 'deployments.write')
	)`

func shortIdentifier(identifier string) string {
	identifier = strings.ReplaceAll(identifier, "-", "")
	if len(identifier) > 6 {
		return identifier[:6]
	}
	return identifier
}

func boundedError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 2000 {
		return message[:2000]
	}
	return message
}
