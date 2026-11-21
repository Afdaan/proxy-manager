package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/afdaan/proxy-manager/internal/model"
	"github.com/afdaan/proxy-manager/internal/store"
)

type createProxyHostRequest struct {
	ApplicationID   string                `json:"applicationId"`
	Name            string                `json:"name"`
	PrimaryDomain   string                `json:"primaryDomain"`
	DomainAliases   []string              `json:"domainAliases"`
	UpstreamURL     string                `json:"upstreamUrl"`
	Upstreams       []model.Upstream      `json:"upstreams"`
	LoadBalancing   string                `json:"loadBalancing"`
	RateLimit       model.RateLimitPolicy `json:"rateLimit"`
	WAF             model.WAFPolicy       `json:"waf"`
	SecurityHeaders *bool                 `json:"securityHeaders"`
	MaxBodySizeMB   int                   `json:"maxBodySizeMb"`
}

type updateProxyHostRequest struct {
	Name            string                `json:"name"`
	Enabled         bool                  `json:"enabled"`
	PrimaryDomain   string                `json:"primaryDomain"`
	DomainAliases   []string              `json:"domainAliases"`
	Upstreams       []model.Upstream      `json:"upstreams"`
	LoadBalancing   string                `json:"loadBalancing"`
	RateLimit       model.RateLimitPolicy `json:"rateLimit"`
	WAF             model.WAFPolicy       `json:"waf"`
	SecurityHeaders bool                  `json:"securityHeaders"`
	MaxBodySizeMB   int                   `json:"maxBodySizeMb"`
	Version         int64                 `json:"version"`
}

type deleteProxyHostRequest struct {
	Version int64 `json:"version"`
}

func (server Server) applications(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	applications, err := server.Store.ListApplications(request.Context(), auth.Session.User.ID)
	if err != nil {
		server.internalError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"applications": applications})
}

func (server Server) proxyHosts(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	hosts, err := server.Store.ListProxyHosts(request.Context(), auth.Session.User.ID)
	if err != nil {
		server.internalError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"proxyHosts": hosts})
}

func (server Server) proxyHost(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	host, err := server.Store.GetProxyHost(request.Context(), auth.Session.User.ID, strings.TrimSpace(request.PathValue("proxyHostID")))
	if server.writeProxyHostError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"proxyHost": host})
}

func (server Server) createProxyHost(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	var input createProxyHostRequest
	if err := decodeJSON(response, request, &input, 32<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	upstreams := input.Upstreams
	if len(upstreams) == 0 && strings.TrimSpace(input.UpstreamURL) != "" {
		upstreams = []model.Upstream{{URL: input.UpstreamURL}}
	}
	securityHeaders := true
	if input.SecurityHeaders != nil {
		securityHeaders = *input.SecurityHeaders
	}
	result, err := server.Store.CreateProxyHost(request.Context(), auth.Session.User.ID, store.CreateProxyHostParams{
		ApplicationID:   strings.TrimSpace(input.ApplicationID),
		Name:            input.Name,
		PrimaryDomain:   input.PrimaryDomain,
		DomainAliases:   input.DomainAliases,
		Upstreams:       upstreams,
		LoadBalancing:   input.LoadBalancing,
		RateLimit:       input.RateLimit,
		WAF:             input.WAF,
		SecurityHeaders: securityHeaders,
		MaxBodySizeMB:   input.MaxBodySizeMB,
		RequestID:       requestIDFromContext(request.Context()),
		SourceIP:        clientIP(request),
	})
	if server.writeProxyHostError(response, request, err) {
		return
	}
	server.writeReconciledProxyHost(response, request, result, http.StatusCreated)
}

func (server Server) updateProxyHost(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	var input updateProxyHostRequest
	if err := decodeJSON(response, request, &input, 32<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := server.Store.UpdateProxyHost(request.Context(), auth.Session.User.ID, store.UpdateProxyHostParams{
		ID:              strings.TrimSpace(request.PathValue("proxyHostID")),
		Name:            input.Name,
		Enabled:         input.Enabled,
		PrimaryDomain:   input.PrimaryDomain,
		DomainAliases:   input.DomainAliases,
		Upstreams:       input.Upstreams,
		LoadBalancing:   input.LoadBalancing,
		RateLimit:       input.RateLimit,
		WAF:             input.WAF,
		SecurityHeaders: input.SecurityHeaders,
		MaxBodySizeMB:   input.MaxBodySizeMB,
		Version:         input.Version,
		RequestID:       requestIDFromContext(request.Context()),
		SourceIP:        clientIP(request),
	})
	if server.writeProxyHostError(response, request, err) {
		return
	}
	server.writeReconciledProxyHost(response, request, result, http.StatusOK)
}

func (server Server) deleteProxyHost(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	var input deleteProxyHostRequest
	if err := decodeJSON(response, request, &input, 4<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := server.Store.DeleteProxyHost(request.Context(), auth.Session.User.ID, store.DeleteProxyHostParams{
		ID:        strings.TrimSpace(request.PathValue("proxyHostID")),
		Version:   input.Version,
		RequestID: requestIDFromContext(request.Context()),
		SourceIP:  clientIP(request),
	})
	if server.writeProxyHostError(response, request, err) {
		return
	}

	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 90*time.Second)
	defer cancel()
	deployment, reconcileErr := server.Reconciler.Reconcile(operationCtx, result.Deployment.ID)
	if reconcileErr != nil {
		slog.Error("reconcile deleted proxy host",
			"request_id", requestIDFromContext(request.Context()),
			"proxy_host_id", request.PathValue("proxyHostID"),
			"deployment_id", result.Deployment.ID,
			"error", reconcileErr,
		)
		server.Reconciler.Wake()
		writeJSON(response, http.StatusAccepted, map[string]any{"deployment": result.Deployment})
		return
	}
	status := http.StatusOK
	if deployment.Status != "succeeded" {
		status = http.StatusAccepted
	}
	writeJSON(response, status, map[string]any{"deployment": deployment})
}

func (server Server) writeReconciledProxyHost(response http.ResponseWriter, request *http.Request, result store.CreateProxyHostResult, successStatus int) {

	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 90*time.Second)
	defer cancel()
	deployment, reconcileErr := server.Reconciler.Reconcile(operationCtx, result.Deployment.ID)
	if reconcileErr != nil {
		slog.Error("reconcile saved proxy host",
			"request_id", requestIDFromContext(request.Context()),
			"proxy_host_id", result.ProxyHost.ID,
			"deployment_id", result.Deployment.ID,
			"error", reconcileErr,
		)
		server.Reconciler.Wake()
		writeJSON(response, http.StatusAccepted, map[string]any{
			"proxyHost":  result.ProxyHost,
			"deployment": result.Deployment,
		})
		return
	}

	result.Deployment = deployment
	switch deployment.Status {
	case "succeeded":
		result.ProxyHost.ConfigurationStatus = "active"
	case "failed":
		result.ProxyHost.ConfigurationStatus = "failed"
		result.ProxyHost.ConfigurationError = deployment.ErrorMessage
	case "retrying":
		result.ProxyHost.ConfigurationStatus = "retrying"
		result.ProxyHost.ConfigurationError = deployment.ErrorMessage
	default:
		result.ProxyHost.ConfigurationStatus = "applying"
	}
	status := successStatus
	if deployment.Status != "succeeded" {
		status = http.StatusAccepted
	}
	writeJSON(response, status, map[string]any{
		"proxyHost":  result.ProxyHost,
		"deployment": deployment,
	})
}

func (server Server) writeProxyHostError(response http.ResponseWriter, request *http.Request, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrForbidden):
		writeError(response, http.StatusForbidden, "permission_denied", "proxy host write permission required")
	case errors.Is(err, store.ErrNotFound):
		writeError(response, http.StatusNotFound, "proxy_host_not_found", "proxy host not found")
	case errors.Is(err, store.ErrStaleVersion):
		writeError(response, http.StatusConflict, "stale_version", "proxy host changed; reload before saving")
	case errors.Is(err, store.ErrConflict):
		writeError(response, http.StatusConflict, "domain_conflict", "domain is already assigned")
	case isValidationError(err):
		writeError(response, http.StatusBadRequest, "invalid_proxy_host", err.Error())
	default:
		server.internalError(response, request, err)
	}
	return true
}

func isValidationError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "must ") ||
		strings.Contains(message, "invalid ") ||
		strings.Contains(message, "not supported") ||
		strings.Contains(message, "scheme")
}
