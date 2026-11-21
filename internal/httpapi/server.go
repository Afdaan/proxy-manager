package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/afdaan/proxy-manager/internal/store"
)

type Server struct {
	AgentSocket  string
	CookieSecure bool
	Reconciler   *Reconciler
	Store        *store.Store
	WebRoot      string
}

func (server Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", server.health)
	mux.HandleFunc("POST /api/v1/auth/login", server.login)
	mux.HandleFunc("GET /api/v1/auth/me", server.authenticate(server.me))
	mux.HandleFunc("POST /api/v1/auth/logout", server.authenticate(server.requireCSRF(server.logout)))
	mux.HandleFunc("GET /api/v1/overview", server.authenticate(server.overview))
	mux.HandleFunc("GET /api/v1/applications", server.authenticate(server.applications))
	mux.HandleFunc("GET /api/v1/proxy-hosts", server.authenticate(server.proxyHosts))
	mux.HandleFunc("POST /api/v1/proxy-hosts", server.authenticate(server.requireCSRF(server.createProxyHost)))
	mux.HandleFunc("GET /api/v1/proxy-hosts/{proxyHostID}", server.authenticate(server.proxyHost))
	mux.HandleFunc("PUT /api/v1/proxy-hosts/{proxyHostID}", server.authenticate(server.requireCSRF(server.updateProxyHost)))
	mux.HandleFunc("DELETE /api/v1/proxy-hosts/{proxyHostID}", server.authenticate(server.requireCSRF(server.deleteProxyHost)))
	mux.HandleFunc("POST /api/v1/proxy-hosts/{proxyHostID}/certificate", server.authenticate(server.requireCSRF(server.issueAutomaticCertificate)))
	mux.HandleFunc("GET /api/v1/deployments", server.authenticate(server.deployments))
	mux.HandleFunc("POST /api/v1/deployments", server.authenticate(server.requireCSRF(server.createDeployment)))
	mux.HandleFunc("GET /api/v1/admin/access", server.authenticate(server.accessOverview))
	mux.HandleFunc("POST /api/v1/admin/users", server.authenticate(server.requireCSRF(server.createManagedUser)))
	mux.HandleFunc("PUT /api/v1/admin/users/{userID}", server.authenticate(server.requireCSRF(server.updateManagedUser)))
	mux.HandleFunc("POST /api/v1/admin/users/{userID}/password", server.authenticate(server.requireCSRF(server.resetManagedUserPassword)))
	mux.Handle("/", server.webHandler())
	return requestIDs(securityHeaders(mux))
}

func (server Server) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok", "service": "server"})
}

func (server Server) overview(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	overview, err := server.Store.Overview(request.Context(), auth.Session.User.ID)
	if err != nil {
		server.internalError(response, request, err)
		return
	}

	agentStatus := "offline"
	agentRequest, err := http.NewRequestWithContext(request.Context(), http.MethodGet, "http://agent/v1/health", nil)
	if err == nil {
		if result, requestErr := server.agentClient().Do(agentRequest); requestErr == nil {
			if result.StatusCode == http.StatusOK {
				agentStatus = "online"
			}
			_ = result.Body.Close()
		}
	}

	writeJSON(response, http.StatusOK, map[string]any{
		"agentStatus":  agentStatus,
		"proxyHosts":   overview.ProxyHosts,
		"certificates": overview.Certificates,
		"blockedToday": overview.BlockedToday,
		"features": []map[string]string{
			{"id": "reverse-proxy", "name": "Reverse proxy", "status": "foundation"},
			{"id": "certificates", "name": "Automatic certificates", "status": "foundation"},
			{"id": "rate-limit", "name": "Rate limiting", "status": "renderer-ready"},
			{"id": "waf", "name": "ModSecurity + OWASP CRS", "status": "renderer-ready"},
			{"id": "geoip", "name": "GeoIP policies", "status": "renderer-ready"},
			{"id": "rbac", "name": "Application RBAC", "status": "foundation"},
		},
	})
}

func (server Server) agentClient() *http.Client {
	return newAgentClient(server.AgentSocket)
}

func newAgentClient(socket string) *http.Client {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
	}
	return &http.Client{Transport: transport, Timeout: 40 * time.Second}
}

func (server Server) webHandler() http.Handler {
	root := server.WebRoot
	if root == "" {
		root = "apps/web/dist"
	}
	fileServer := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestedFile := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		filePath := filepath.Join(root, requestedFile)
		if information, err := os.Stat(filePath); err == nil && !information.IsDir() {
			fileServer.ServeHTTP(response, request)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/") {
			http.NotFound(response, request)
			return
		}
		http.ServeFile(response, request, filepath.Join(root, "index.html"))
	})
}

func (server Server) internalError(response http.ResponseWriter, request *http.Request, err error) {
	slog.Error("API request failed",
		"request_id", requestIDFromContext(request.Context()),
		"method", request.Method,
		"path", request.URL.Path,
		"error", err,
	)
	writeError(response, http.StatusInternalServerError, "internal_error", "request failed")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "same-origin")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		if strings.HasPrefix(request.URL.Path, "/api/") {
			response.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(response, request)
	})
}

type requestIDKey struct{}

func requestIDs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		identifierBytes := make([]byte, 16)
		if _, err := rand.Read(identifierBytes); err != nil {
			writeError(response, http.StatusInternalServerError, "internal_error", "request failed")
			return
		}
		identifier := hex.EncodeToString(identifierBytes)
		response.Header().Set("X-Request-ID", identifier)
		next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), requestIDKey{}, identifier)))
	})
}

func requestIDFromContext(ctx context.Context) string {
	identifier, _ := ctx.Value(requestIDKey{}).(string)
	return identifier
}

func clientIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return ""
	}
	remoteIP := net.ParseIP(host)
	if remoteIP != nil && remoteIP.IsLoopback() {
		forwarded := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-For"), ",")[0])
		if net.ParseIP(forwarded) != nil {
			return forwarded
		}
	}
	return host
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any, limit int64) error {
	defer request.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"code": code, "error": message})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
