package store

import (
	"crypto/sha256"
	"testing"
)

func TestVerifyCSRF(t *testing.T) {
	token := "correct-token-string"
	hash := sha256.Sum256([]byte(token))

	if !VerifyCSRF(token, hash[:]) {
		t.Fatal("expected CSRF token to verify")
	}
	if VerifyCSRF("wrong-token", hash[:]) {
		t.Fatal("expected invalid CSRF token to be rejected")
	}
}

func TestNormalizeEmail(t *testing.T) {
	email, err := normalizeEmail("  Admin@Example.COM ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if email != "admin@example.com" {
		t.Fatalf("expected lowercase trimmed email, got %s", email)
	}
	if _, err := normalizeEmail("not-an-email"); err == nil {
		t.Fatal("expected invalid email to fail")
	}
}
