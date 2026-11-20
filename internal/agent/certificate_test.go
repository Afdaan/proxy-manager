package agent

import (
	"context"
	"slices"
	"testing"
)

func TestCertificateIssueUsesValidatedArguments(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	manager := CertificateManager{
		Runner:        runner,
		CertbotBinary: "/usr/bin/certbot",
		ACMEWebroot:   "/var/lib/proxy-manager/acme",
	}
	request := CertificateRequest{
		Domains: []string{"api.example.com", "app.example.com", "api.example.com"},
		Email:   "admin@example.com",
		Staging: true,
	}

	if err := manager.Issue(context.Background(), request); err != nil {
		t.Fatalf("issue certificate: %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("expected one certbot command, got %d", len(runner.commands))
	}
	command := runner.commands[0]
	if command.name != "/usr/bin/certbot" {
		t.Fatalf("unexpected command %q", command.name)
	}
	expectedDomains := []string{
		"--domain", "api.example.com",
		"--domain", "app.example.com",
	}
	if !containsSequence(command.arguments, expectedDomains) {
		t.Fatalf("missing sorted unique domains in %v", command.arguments)
	}
	if !slices.Contains(command.arguments, "--staging") {
		t.Fatalf("missing staging argument in %v", command.arguments)
	}
}

func TestCertificateIssueRejectsInvalidDomain(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	manager := CertificateManager{Runner: runner}
	request := CertificateRequest{Domains: []string{"$(touch /tmp/nope)"}, Email: "admin@example.com"}

	if err := manager.Issue(context.Background(), request); err == nil {
		t.Fatal("expected invalid domain to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatal("invalid input reached command runner")
	}
}

func containsSequence(values, sequence []string) bool {
	if len(sequence) > len(values) {
		return false
	}
	for index := 0; index <= len(values)-len(sequence); index++ {
		if slices.Equal(values[index:index+len(sequence)], sequence) {
			return true
		}
	}
	return false
}
