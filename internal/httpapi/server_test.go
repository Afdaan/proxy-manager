package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnauthenticatedRoutesRequireLogin(t *testing.T) {
	server := Server{}.Routes()

	for _, path := range []string{"/api/v1/auth/me", "/api/v1/overview", "/api/v1/applications", "/api/v1/proxy-hosts", "/api/v1/deployments"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for %s, got %d", path, recorder.Code)
		}
	}
}

func TestHealthDoesNotRequireAuthentication(t *testing.T) {
	server := Server{}.Routes()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
}

func TestRequireCSRFRejectsMissingToken(t *testing.T) {
	server := Server{}
	handler := server.requireCSRF(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	auth := authContext{CSRFHash: []byte("hash-bytes")}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/proxy-hosts", nil)
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, auth))
	recorder := httptest.NewRecorder()

	handler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", recorder.Code)
	}
}
