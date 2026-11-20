package main

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/afdaan/proxy-manager/internal/agent"
	"github.com/afdaan/proxy-manager/internal/nginx"
)

func main() {
	token := os.Getenv("PROXY_MANAGER_AGENT_TOKEN")
	if token == "" {
		slog.Error("PROXY_MANAGER_AGENT_TOKEN is required")
		os.Exit(1)
	}
	socketPath := valueOrDefault("PROXY_MANAGER_AGENT_SOCKET", "/run/proxy-manager/agent.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o750); err != nil {
		slog.Error("create socket directory", "error", err)
		os.Exit(1)
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		slog.Error("listen on agent socket", "error", err)
		os.Exit(1)
	}
	defer listener.Close()
	if err := os.Chmod(socketPath, 0o660); err != nil {
		slog.Error("set socket permissions", "error", err)
		os.Exit(1)
	}

	applier := &agent.Applier{
		Renderer:         nginx.Renderer{},
		Runner:           agent.ExecRunner{},
		StateDir:         valueOrDefault("PROXY_MANAGER_STATE_DIR", "/var/lib/proxy-manager"),
		ActiveConfigPath: valueOrDefault("PROXY_MANAGER_ACTIVE_CONFIG", "/etc/nginx/proxy-manager/active.conf"),
		NginxBinary:      valueOrDefault("PROXY_MANAGER_NGINX_BIN", "/usr/sbin/nginx"),
	}
	certificateManager := agent.CertificateManager{
		Runner:        agent.ExecRunner{},
		CertbotBinary: valueOrDefault("PROXY_MANAGER_CERTBOT_BIN", "/usr/bin/certbot"),
		ACMEWebroot:   valueOrDefault("PROXY_MANAGER_ACME_WEBROOT", "/var/lib/proxy-manager/acme"),
	}
	server := &http.Server{
		Handler: agent.HTTPHandler{
			Token:        token,
			Applier:      applier,
			Certificates: certificateManager,
		}.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-shutdownSignal()
		_ = server.Close()
	}()
	slog.Info("agent listening", "socket", socketPath)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}

func shutdownSignal() <-chan os.Signal {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	return signals
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
