package agent

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/afdaan/proxy-manager/internal/model"
)

type CertificateRequest struct {
	Domains []string `json:"domains"`
	Email   string   `json:"email"`
	Staging bool     `json:"staging"`
}

type CertificateManager struct {
	Runner         CommandRunner
	CertbotBinary  string
	ACMEWebroot    string
	CommandTimeout time.Duration
}

func (manager CertificateManager) Issue(ctx context.Context, request CertificateRequest) error {
	if manager.Runner == nil {
		return errors.New("command runner is required")
	}
	if len(request.Domains) == 0 || len(request.Domains) > 20 {
		return errors.New("certificate request requires 1 to 20 domains")
	}
	address, err := mail.ParseAddress(strings.TrimSpace(request.Email))
	if err != nil || address.Address != request.Email {
		return errors.New("certificate request requires a valid email")
	}

	domains := make([]string, 0, len(request.Domains))
	for _, value := range request.Domains {
		domain, err := model.NormalizeDomain(value)
		if err != nil {
			return err
		}
		domains = append(domains, domain)
	}
	slices.Sort(domains)
	domains = slices.Compact(domains)

	arguments := []string{
		"certonly",
		"--webroot",
		"--webroot-path", manager.webroot(),
		"--non-interactive",
		"--agree-tos",
		"--keep-until-expiring",
		"--email", request.Email,
	}
	if request.Staging {
		arguments = append(arguments, "--staging")
	}
	for _, domain := range domains {
		arguments = append(arguments, "--domain", domain)
	}

	commandContext, cancel := context.WithTimeout(ctx, manager.timeout())
	defer cancel()
	if err := manager.Runner.Run(commandContext, manager.binary(), arguments...); err != nil {
		return fmt.Errorf("issue certificate: %w", err)
	}
	return nil
}

func (manager CertificateManager) binary() string {
	if manager.CertbotBinary == "" {
		return "/usr/bin/certbot"
	}
	return manager.CertbotBinary
}

func (manager CertificateManager) webroot() string {
	if manager.ACMEWebroot == "" {
		return "/var/lib/proxy-manager/acme"
	}
	return manager.ACMEWebroot
}

func (manager CertificateManager) timeout() time.Duration {
	if manager.CommandTimeout == 0 {
		return 2 * time.Minute
	}
	return manager.CommandTimeout
}
