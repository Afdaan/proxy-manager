package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	database "github.com/afdaan/proxy-manager/db"
	"github.com/afdaan/proxy-manager/internal/httpapi"
	"github.com/afdaan/proxy-manager/internal/store"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	databaseURL := os.Getenv("PROXY_MANAGER_DATABASE_URL")
	if databaseURL == "" {
		slog.Error("PROXY_MANAGER_DATABASE_URL is required")
		os.Exit(1)
	}
	dataStore, err := store.Open(ctx, databaseURL)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer dataStore.Close()
	if err := database.Migrate(ctx, dataStore.Pool); err != nil {
		slog.Error("migrate database", "error", err)
		os.Exit(1)
	}
	if err := bootstrap(ctx, dataStore); err != nil {
		slog.Error("bootstrap administrator", "error", err)
		os.Exit(1)
	}

	address := valueOrDefault("PROXY_MANAGER_HTTP_ADDRESS", "127.0.0.1:8080")
	server := &http.Server{
		Addr: address,
		Handler: httpapi.Server{
			AgentSocket:  valueOrDefault("PROXY_MANAGER_AGENT_SOCKET", "/run/proxy-manager/agent.sock"),
			AgentToken:   os.Getenv("PROXY_MANAGER_AGENT_TOKEN"),
			CookieSecure: boolValue("PROXY_MANAGER_COOKIE_SECURE", true),
			Store:        dataStore,
			WebRoot:      valueOrDefault("PROXY_MANAGER_WEB_ROOT", "apps/web/dist"),
		}.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      100 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		<-signals
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
		}
	}()
	slog.Info("server listening", "address", address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func bootstrap(ctx context.Context, dataStore *store.Store) error {
	usersExist, err := dataStore.UsersExist(ctx)
	if err != nil {
		return err
	}
	if usersExist {
		return nil
	}
	email := os.Getenv("PROXY_MANAGER_BOOTSTRAP_EMAIL")
	password := os.Getenv("PROXY_MANAGER_BOOTSTRAP_PASSWORD")
	if email == "" || password == "" {
		return errors.New("empty database requires PROXY_MANAGER_BOOTSTRAP_EMAIL and PROXY_MANAGER_BOOTSTRAP_PASSWORD")
	}
	created, err := dataStore.BootstrapAdmin(
		ctx,
		email,
		valueOrDefault("PROXY_MANAGER_BOOTSTRAP_NAME", "Administrator"),
		password,
	)
	if err != nil {
		return err
	}
	if created {
		slog.Info("bootstrap administrator created", "email", email)
	}
	return nil
}

func boolValue(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		slog.Error("invalid boolean environment variable", "key", key, "value", value)
		os.Exit(1)
	}
	return parsed
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
