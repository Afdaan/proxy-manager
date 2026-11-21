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

const gatewayReconcileLockID = 742198534

type Reconciler struct {
	AgentToken string
	Store      *store.Store
	client     *http.Client
	serial     chan struct{}
	wake       chan struct{}
}

type agentCallError struct {
	err       error
	retryable bool
}

func (failure *agentCallError) Error() string {
	return failure.err.Error()
}

func NewReconciler(dataStore *store.Store, agentSocket, agentToken string) *Reconciler {
	return &Reconciler{
		AgentToken: agentToken,
		Store:      dataStore,
		client:     newAgentClient(agentSocket),
		serial:     make(chan struct{}, 1),
		wake:       make(chan struct{}, 1),
	}
}

func (reconciler *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	reconciler.Wake()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-reconciler.wake:
		}
		reconciler.processPending(ctx)
	}
}

func (reconciler *Reconciler) Wake() {
	select {
	case reconciler.wake <- struct{}{}:
	default:
	}
}

func (reconciler *Reconciler) processPending(ctx context.Context) {
	for ctx.Err() == nil {
		deploymentID, err := reconciler.Store.NextDeploymentID(ctx)
		if err != nil {
			slog.Error("find pending gateway configuration", "error", err)
			return
		}
		if deploymentID == "" {
			return
		}
		if _, err := reconciler.Reconcile(ctx, deploymentID); err != nil {
			slog.Error("reconcile gateway configuration", "deployment_id", deploymentID, "error", err)
			return
		}
	}
}

func (reconciler *Reconciler) Reconcile(ctx context.Context, deploymentID string) (store.Deployment, error) {
	select {
	case reconciler.serial <- struct{}{}:
		defer func() { <-reconciler.serial }()
	case <-ctx.Done():
		return store.Deployment{}, ctx.Err()
	}

	connection, err := reconciler.Store.Pool.Acquire(ctx)
	if err != nil {
		return store.Deployment{}, fmt.Errorf("acquire reconcile connection: %w", err)
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", gatewayReconcileLockID); err != nil {
		return store.Deployment{}, fmt.Errorf("lock gateway reconciliation: %w", err)
	}
	defer connection.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", gatewayReconcileLockID) //nolint:errcheck

	prepared, err := reconciler.Store.PrepareDeployment(ctx, deploymentID)
	if err != nil {
		return store.Deployment{}, err
	}
	if !prepared.Process {
		return prepared.Deployment, nil
	}

	if err := reconciler.callAgent(ctx, "/v1/deployments/validate", prepared.Config); err != nil {
		return reconciler.recordFailure(ctx, prepared.Deployment, "validating", err)
	}
	deployment, err := reconciler.Store.TransitionDeployment(ctx, deploymentID, "validating", "applying", "")
	if err != nil {
		return store.Deployment{}, err
	}
	if err := reconciler.callAgent(ctx, "/v1/deployments/apply", prepared.Config); err != nil {
		return reconciler.recordFailure(ctx, deployment, "applying", err)
	}
	return reconciler.Store.CompleteDeployment(ctx, deploymentID)
}

func (reconciler *Reconciler) recordFailure(ctx context.Context, deployment store.Deployment, expectedStatus string, cause error) (store.Deployment, error) {
	var agentFailure *agentCallError
	if errors.As(cause, &agentFailure) && agentFailure.retryable {
		retryAt := time.Now().UTC().Add(30 * time.Second)
		retrying, err := reconciler.Store.RetryDeployment(ctx, deployment.ID, expectedStatus, cause.Error(), retryAt)
		if err == nil {
			reconciler.Wake()
		}
		return retrying, err
	}
	return reconciler.Store.TransitionDeployment(ctx, deployment.ID, expectedStatus, "failed", cause.Error())
}

func (reconciler *Reconciler) callAgent(ctx context.Context, path string, config model.GatewayConfig) error {
	if reconciler.AgentToken == "" {
		return &agentCallError{err: errors.New("agent token is not configured"), retryable: true}
	}
	payload, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode agent request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent"+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create agent request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+reconciler.AgentToken)
	request.Header.Set("Content-Type", "application/json")

	response, err := reconciler.client.Do(request)
	if err != nil {
		return &agentCallError{err: fmt.Errorf("agent unavailable: %w", err), retryable: true}
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}

	contents, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var responseError struct {
		Error string `json:"error"`
	}
	message := fmt.Sprintf("agent rejected configuration with status %d", response.StatusCode)
	if json.Unmarshal(contents, &responseError) == nil && strings.TrimSpace(responseError.Error) != "" {
		message = "agent rejected configuration: " + responseError.Error
	}
	return &agentCallError{
		err:       errors.New(message),
		retryable: response.StatusCode >= http.StatusInternalServerError || response.StatusCode == http.StatusUnauthorized,
	}
}
