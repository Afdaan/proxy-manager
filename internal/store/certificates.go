package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/afdaan/proxy-manager/internal/model"
	"github.com/jackc/pgx/v5"
)

type PrepareCertificateParams struct {
	ProxyHostID string
	Email       string
	Version     int64
	RequestID   string
	SourceIP    string
}

type CertificatePreparation struct {
	ProxyHostID string
	Domains     []string
	Email       string
	Version     int64
	Deployment  Deployment
	Config      model.GatewayConfig
}

func (store *Store) PrepareAutomaticCertificate(ctx context.Context, userID string, params PrepareCertificateParams) (CertificatePreparation, error) {
	if params.Version < 1 {
		return CertificatePreparation{}, errors.New("version must be at least 1")
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CertificatePreparation{}, fmt.Errorf("begin certificate preparation: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck
	if err := lockGatewayMutation(ctx, transaction); err != nil {
		return CertificatePreparation{}, err
	}
	detail, err := loadProxyHostDetail(ctx, transaction, params.ProxyHostID, true)
	if err != nil {
		return CertificatePreparation{}, err
	}
	allowed, err := hasApplicationPermission(ctx, transaction, userID, detail.ApplicationID, "security.write")
	if err != nil {
		return CertificatePreparation{}, err
	}
	if !allowed {
		return CertificatePreparation{}, ErrForbidden
	}
	if detail.Version != params.Version {
		return CertificatePreparation{}, ErrStaleVersion
	}

	policy := model.TLSPolicy{
		Mode:         "automatic",
		Email:        strings.TrimSpace(params.Email),
		Ready:        false,
		Status:       "pending",
		RedirectHTTP: true,
	}
	if err := policy.Validate(); err != nil {
		return CertificatePreparation{}, err
	}
	encodedPolicy, _ := json.Marshal(policy)
	command, err := transaction.Exec(ctx, `
		UPDATE proxy_hosts
		SET tls_policy = $2,
		    version = version + 1,
		    updated_at = now()
		WHERE id = $1 AND version = $3`, params.ProxyHostID, encodedPolicy, params.Version)
	if err != nil {
		return CertificatePreparation{}, fmt.Errorf("prepare automatic certificate: %w", err)
	}
	if command.RowsAffected() == 0 {
		return CertificatePreparation{}, ErrStaleVersion
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_logs (
		    actor_user_id, application_id, action, resource_type, resource_id, request_id, source_ip, details
		) VALUES ($1, $2, 'certificate.request', 'proxy_host', $3, $4, NULLIF($5, '')::inet, $6)`,
		userID,
		detail.ApplicationID,
		params.ProxyHostID,
		params.RequestID,
		params.SourceIP,
		map[string]any{"domains": append([]string{detail.PrimaryDomain}, detail.DomainAliases...), "email": policy.Email},
	); err != nil {
		return CertificatePreparation{}, fmt.Errorf("write certificate request audit log: %w", err)
	}
	deployment, config, err := createDeploymentRecord(ctx, transaction, userID, CreateDeploymentParams{
		RequestID: params.RequestID,
		SourceIP:  params.SourceIP,
	})
	if err != nil {
		return CertificatePreparation{}, err
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE proxy_hosts SET desired_generation = $2 WHERE id = $1`, params.ProxyHostID, deployment.Generation); err != nil {
		return CertificatePreparation{}, fmt.Errorf("record certificate challenge generation: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return CertificatePreparation{}, fmt.Errorf("commit certificate preparation: %w", err)
	}
	return CertificatePreparation{
		ProxyHostID: params.ProxyHostID,
		Domains:     append([]string{detail.PrimaryDomain}, detail.DomainAliases...),
		Email:       policy.Email,
		Version:     params.Version + 1,
		Deployment:  deployment,
		Config:      config,
	}, nil
}

func (store *Store) FailAutomaticCertificate(ctx context.Context, userID, proxyHostID string, expectedVersion int64, cause, requestID, sourceIP string) error {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin certificate failure: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck
	detail, err := loadProxyHostDetail(ctx, transaction, proxyHostID, true)
	if err != nil {
		return err
	}
	if detail.Version != expectedVersion {
		return ErrStaleVersion
	}
	detail.TLS.Ready = false
	detail.TLS.Status = "failed"
	detail.TLS.Error = boundedError(cause)
	encodedPolicy, _ := json.Marshal(detail.TLS)
	if _, err := transaction.Exec(ctx, `
		UPDATE proxy_hosts
		SET tls_policy = $2, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $3`, proxyHostID, encodedPolicy, expectedVersion); err != nil {
		return fmt.Errorf("record certificate failure: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_logs (
		    actor_user_id, application_id, action, resource_type, resource_id, request_id, source_ip, details
		) VALUES ($1, $2, 'certificate.failed', 'proxy_host', $3, $4, NULLIF($5, '')::inet, $6)`,
		userID, detail.ApplicationID, proxyHostID, requestID, sourceIP, map[string]any{"error": detail.TLS.Error}); err != nil {
		return fmt.Errorf("write certificate failure audit log: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit certificate failure: %w", err)
	}
	return nil
}

func (store *Store) ActivateAutomaticCertificate(ctx context.Context, userID, proxyHostID string, expectedVersion int64, requestID, sourceIP string) (CreateProxyHostResult, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CreateProxyHostResult{}, fmt.Errorf("begin certificate activation: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck
	if err := lockGatewayMutation(ctx, transaction); err != nil {
		return CreateProxyHostResult{}, err
	}
	detail, err := loadProxyHostDetail(ctx, transaction, proxyHostID, true)
	if err != nil {
		return CreateProxyHostResult{}, err
	}
	if detail.Version != expectedVersion {
		return CreateProxyHostResult{}, ErrStaleVersion
	}
	if detail.TLS.Mode != "automatic" {
		return CreateProxyHostResult{}, errors.New("proxy host is not configured for automatic TLS")
	}
	detail.TLS.Ready = true
	detail.TLS.Status = "active"
	detail.TLS.Error = ""
	encodedPolicy, _ := json.Marshal(detail.TLS)
	if _, err := transaction.Exec(ctx, `
		UPDATE proxy_hosts
		SET tls_policy = $2, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $3`, proxyHostID, encodedPolicy, expectedVersion); err != nil {
		return CreateProxyHostResult{}, fmt.Errorf("activate automatic certificate: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_logs (
		    actor_user_id, application_id, action, resource_type, resource_id, request_id, source_ip, details
		) VALUES ($1, $2, 'certificate.activate', 'proxy_host', $3, $4, NULLIF($5, '')::inet, $6)`,
		userID, detail.ApplicationID, proxyHostID, requestID, sourceIP, map[string]any{"domains": append([]string{detail.PrimaryDomain}, detail.DomainAliases...)}); err != nil {
		return CreateProxyHostResult{}, fmt.Errorf("write certificate activation audit log: %w", err)
	}
	deployment, config, err := createDeploymentRecord(ctx, transaction, userID, CreateDeploymentParams{
		RequestID: requestID,
		SourceIP:  sourceIP,
	})
	if err != nil {
		return CreateProxyHostResult{}, err
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE proxy_hosts SET desired_generation = $2 WHERE id = $1`, proxyHostID, deployment.Generation); err != nil {
		return CreateProxyHostResult{}, fmt.Errorf("record certificate activation generation: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return CreateProxyHostResult{}, fmt.Errorf("commit certificate activation: %w", err)
	}
	return CreateProxyHostResult{
		ProxyHost: ProxyHost{
			ID:                  detail.ID,
			ApplicationID:       detail.ApplicationID,
			Application:         detail.Application,
			Name:                detail.Name,
			Enabled:             detail.Enabled,
			PrimaryDomain:       detail.PrimaryDomain,
			UpstreamURL:         firstUpstreamURL(detail.Upstreams),
			UpstreamCount:       len(detail.Upstreams),
			LoadBalancing:       detail.LoadBalancing,
			TLSMode:             "automatic",
			WAFMode:             detail.WAF.Mode,
			RateLimitEnabled:    detail.RateLimit.Enabled,
			CanWrite:            true,
			ConfigurationStatus: "applying",
			Version:             expectedVersion + 1,
		},
		Deployment: deployment,
		Config:     config,
	}, nil
}
