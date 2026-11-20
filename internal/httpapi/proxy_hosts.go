package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/afdaan/proxy-manager/internal/store"
)

type createProxyHostRequest struct {
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
	PrimaryDomain string `json:"primaryDomain"`
	UpstreamURL   string `json:"upstreamUrl"`
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
	host, err := server.Store.CreateProxyHost(request.Context(), auth.Session.User.ID, store.CreateProxyHostParams{
		ApplicationID: strings.TrimSpace(input.ApplicationID),
		Name:          input.Name,
		PrimaryDomain: input.PrimaryDomain,
		UpstreamURL:   input.UpstreamURL,
		RequestID:     requestIDFromContext(request.Context()),
		SourceIP:      clientIP(request),
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
	writeJSON(response, http.StatusCreated, map[string]any{"proxyHost": host})
}

func isValidationError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "must ") ||
		strings.Contains(message, "invalid ") ||
		strings.Contains(message, "not supported") ||
		strings.Contains(message, "scheme")
}
