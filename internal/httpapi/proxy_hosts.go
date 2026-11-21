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
	if errors.Is(err, store.ErrForbidden) {
		writeError(response, http.StatusForbidden, "permission_denied", "proxy host write permission required")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(response, http.StatusConflict, "domain_conflict", "domain is already assigned")
		return
	}
	if err != nil {
		if isValidationError(err) {
			writeError(response, http.StatusBadRequest, "invalid_proxy_host", err.Error())
			return
		}
		server.internalError(response, request, err)
		return
	}

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
	status := http.StatusCreated
	if deployment.Status != "succeeded" {
		status = http.StatusAccepted
	}
	writeJSON(response, status, map[string]any{
		"proxyHost":  result.ProxyHost,
		"deployment": deployment,
	})
}

func isValidationError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "must ") ||
		strings.Contains(message, "invalid ") ||
		strings.Contains(message, "not supported") ||
		strings.Contains(message, "scheme")
}
