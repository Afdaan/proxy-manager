package nginx

import (
	"strings"
	"testing"

	"github.com/afdaan/proxy-manager/internal/model"
)

func TestRenderGatewayConfig(t *testing.T) {
	t.Parallel()

	config := model.GatewayConfig{
		Generation: 1,
		Revision:   "rev-001",
		Sites: []model.Site{{
			ID:              "billing",
			Name:            "Billing",
			Enabled:         true,
			Domains:         []string{"billing.example.com"},
			Upstreams:       []model.Upstream{{URL: "http://10.20.0.15:8080"}},
			TLS:             model.TLSPolicy{Mode: "automatic", Email: "admin@example.com", Ready: true, RedirectHTTP: true},
			RateLimit:       model.RateLimitPolicy{Enabled: true, Requests: 60, PeriodSeconds: 60, Burst: 20, NoDelay: true},
			WAF:             model.WAFPolicy{Mode: "detect", ParanoiaLevel: 1},
			Geo:             model.GeoPolicy{Mode: "block", Countries: []string{"RU"}},
			SecurityHeaders: true,
		}},
	}

	output, err := (Renderer{}).Render(config)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, expected := range []string{
		"limit_req_zone $binary_remote_addr zone=rate_billing:10m rate=60r/m;",
		"server_name billing.example.com;",
		"proxy_pass http://pm_billing;",
		"modsecurity_rules_file /etc/proxy-manager/waf/detection-only.conf;",
		"RU 1;",
		"return 308 https://$host$request_uri;",
	} {
		if !strings.Contains(string(output), expected) {
			t.Errorf("rendered config missing %q\n%s", expected, output)
		}
	}
}

func TestAutomaticTLSStartsWithHTTPChallengeOnly(t *testing.T) {
	t.Parallel()

	config := model.GatewayConfig{
		Generation: 1,
		Revision:   "certificate-bootstrap",
		Sites: []model.Site{{
			ID:        "bootstrap",
			Name:      "Bootstrap",
			Enabled:   true,
			Domains:   []string{"bootstrap.example.com"},
			Upstreams: []model.Upstream{{URL: "http://10.20.0.16:8080"}},
			TLS:       model.TLSPolicy{Mode: "automatic", Email: "admin@example.com", RedirectHTTP: true},
		}},
	}

	output, err := (Renderer{}).Render(config)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(output), "listen 443 ssl") {
		t.Fatalf("automatic TLS must not activate before a certificate exists\n%s", output)
	}
	if !strings.Contains(string(output), ".well-known/acme-challenge") {
		t.Fatalf("bootstrap config must expose the ACME challenge\n%s", output)
	}
}

func TestRejectsDuplicateDomain(t *testing.T) {
	t.Parallel()

	config := model.GatewayConfig{
		Generation: 1,
		Revision:   "rev-duplicate",
		Sites: []model.Site{
			{ID: "one", Name: "One", Domains: []string{"app.example.com"}},
			{ID: "two", Name: "Two", Domains: []string{"app.example.com"}},
		},
	}

	if _, err := (Renderer{}).Render(config); err == nil {
		t.Fatal("expected duplicate domain to fail validation")
	}
}
