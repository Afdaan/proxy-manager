package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/afdaan/proxy-manager/internal/model"
)

func TestAgentValidationErrorIsNotRetried(t *testing.T) {
	t.Parallel()

	socket := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on agent socket: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]string{"error": "invalid generated configuration"})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	reconciler := NewReconciler(nil, socket, "token")
	err = reconciler.callAgent(context.Background(), "/v1/deployments/validate", model.GatewayConfig{})
	var failure *agentCallError
	if !errors.As(err, &failure) {
		t.Fatalf("expected agent call error, got %v", err)
	}
	if failure.retryable {
		t.Fatal("validation error must require user correction")
	}
}

func TestUnavailableAgentIsRetried(t *testing.T) {
	t.Parallel()

	reconciler := NewReconciler(nil, filepath.Join(t.TempDir(), "missing.sock"), "token")
	err := reconciler.callAgent(context.Background(), "/v1/deployments/validate", model.GatewayConfig{})
	var failure *agentCallError
	if !errors.As(err, &failure) {
		t.Fatalf("expected agent call error, got %v", err)
	}
	if !failure.retryable {
		t.Fatal("unavailable agent must be retried")
	}
}
