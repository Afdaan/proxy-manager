package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

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
	deployment, _, err := server.Store.CreateDeployment(request.Context(), auth.Session.User.ID, store.CreateDeploymentParams{
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
	deployment, err = server.Reconciler.Reconcile(operationCtx, deployment.ID)
	if err != nil {
		server.internalError(response, request, err)
		return
	}
	status := http.StatusCreated
	if deployment.Status == "retrying" {
		status = http.StatusAccepted
	}
	writeJSON(response, status, map[string]any{"deployment": deployment})
}
