package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/afdaan/proxy-manager/internal/model"
	"github.com/afdaan/proxy-manager/internal/store"
)

func (server Server) deployments(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	deployments, err := server.Store.ListDeployments(request.Context(), auth.Session.User.ID, 20)
	if errors.Is(err, store.ErrForbidden) {
		writeError(response, http.StatusForbidden, "permission_denied", "deployment permission required")
		return
	}
	if err != nil {
		server.internalError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"deployments": deployments})
}

func (server Server) createDeployment(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	deployment, config, err := server.Store.CreateDeployment(request.Context(), auth.Session.User.ID, store.CreateDeploymentParams{
		RequestID: requestIDFromContext(request.Context()),
		SourceIP:  clientIP(request),
	})
	if errors.Is(err, store.ErrForbidden) {
		writeError(response, http.StatusForbidden, "permission_denied", "deployment permission required")
		return
	}
	if err != nil {
		server.internalError(response, request, err)
		return
	}
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 90*time.Second)
	defer cancel()

	deployment, err = server.Store.TransitionDeployment(operationCtx, deployment.ID, "queued", "validating", "")
	if err != nil {
		server.internalError(response, request, err)
		return
	}
	if err := server.callAgent(operationCtx, "/v1/deployments/validate", config); err != nil {
		deployment = server.failDeployment(operationCtx, requestIDFromContext(request.Context()), deployment, "validating", err)
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "deployment_failed", "error": err.Error(), "deployment": deployment})
		return
	}

	deployment, err = server.Store.TransitionDeployment(operationCtx, deployment.ID, "validating", "applying", "")
	if err != nil {
		server.internalError(response, request, err)
		return
	}
	if err := server.callAgent(operationCtx, "/v1/deployments/apply", config); err != nil {
		deployment = server.failDeployment(operationCtx, requestIDFromContext(request.Context()), deployment, "applying", err)
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "deployment_failed", "error": err.Error(), "deployment": deployment})
		return
	}

	deployment, err = server.Store.TransitionDeployment(operationCtx, deployment.ID, "applying", "succeeded", "")
	if err != nil {
		server.internalError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"deployment": deployment})
}

func (server Server) failDeployment(ctx context.Context, requestID string, deployment store.Deployment, expectedStatus string, cause error) store.Deployment {
	failed, err := server.Store.TransitionDeployment(ctx, deployment.ID, expectedStatus, "failed", cause.Error())
	if err != nil {
		slog.Error("record failed deployment",
			"request_id", requestID,
			"deployment_id", deployment.ID,
			"error", err,
		)
		return deployment
	}
	return failed
}

func (server Server) callAgent(ctx context.Context, path string, config model.GatewayConfig) error {
	if server.AgentToken == "" {
		return errors.New("agent token is not configured")
	}
	payload, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode agent request: %w", err)
	}
	agentRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent"+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create agent request: %w", err)
	}
	agentRequest.Header.Set("Authorization", "Bearer "+server.AgentToken)
	agentRequest.Header.Set("Content-Type", "application/json")
	result, err := server.agentClient().Do(agentRequest)
	if err != nil {
		return fmt.Errorf("agent unavailable: %w", err)
	}
	defer result.Body.Close()
	if result.StatusCode >= 200 && result.StatusCode < 300 {
		return nil
	}
	contents, _ := io.ReadAll(io.LimitReader(result.Body, 64<<10))
	var responseError struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(contents, &responseError) == nil && strings.TrimSpace(responseError.Error) != "" {
		return fmt.Errorf("agent rejected deployment: %s", responseError.Error)
	}
	return fmt.Errorf("agent rejected deployment with status %d", result.StatusCode)
}
