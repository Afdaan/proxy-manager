package nginx

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/afdaan/proxy-manager/internal/model"
)

func TestRenderGatewayConfigAsReadableBundle(t *testing.T) {
	t.Parallel()

	config := testGatewayConfig()
	revisionDir := "/var/lib/proxy-manager/revisions/2026-11-21_03-30-42Z--rev-42--a81c7f"
	bundle, err := (Renderer{}).RenderAt(config, revisionDir)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	paths := bundlePaths(bundle)
	for _, expected := range []string{
		"nginx.conf",
		"manifest.json",
		"globals/00-runtime.conf",
		"globals/10-rate-limit-zones.conf",
		"globals/20-geoip-maps.conf",
	} {
		if !slices.Contains(paths, expected) {
			t.Errorf("bundle missing %q: %v", expected, paths)
		}
	}

	root := string(bundleFile(t, bundle, "nginx.conf"))
	if !strings.Contains(root, filepath.Join(revisionDir, "hosts", "*.conf")) {
		t.Fatalf("root config does not include immutable host files:\n%s", root)
	}

	hostPath := bundleHostPath(t, bundle, "billing.example.com")
	host := string(bundleFile(t, bundle, hostPath))
	for _, expected := range []string{
		"# Proxy host: Billing",
		"server_name billing.example.com api.example.com;",
		"proxy_pass http://pm_billing;",
		"modsecurity_rules_file /etc/proxy-manager/waf/detection-only.conf;",
		"return 308 https://$host$request_uri;",
		"server 10.20.0.16:8080 weight=1 max_fails=3 fail_timeout=10s backup;",
	} {
		if !strings.Contains(host, expected) {
			t.Errorf("host config missing %q\n%s", expected, host)
		}
	}

	rateLimits := string(bundleFile(t, bundle, "globals/10-rate-limit-zones.conf"))
	if !strings.Contains(rateLimits, "limit_req_zone $binary_remote_addr zone=rate_billing:10m rate=60r/m;") {
		t.Fatalf("unexpected rate limit config:\n%s", rateLimits)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	t.Parallel()

	config := testGatewayConfig()
	first, err := (Renderer{}).Render(config)
	if err != nil {
		t.Fatalf("render first bundle: %v", err)
	}
	second, err := (Renderer{}).Render(config)
	if err != nil {
		t.Fatalf("render second bundle: %v", err)
	}
	if len(first.Files) != len(second.Files) {
		t.Fatalf("bundle file count changed: %d != %d", len(first.Files), len(second.Files))
	}
	for index := range first.Files {
		if first.Files[index].Path != second.Files[index].Path || !bytes.Equal(first.Files[index].Contents, second.Files[index].Contents) {
			t.Fatalf("bundle differs at file %d", index)
		}
	}
}

func TestAutomaticTLSStartsWithHTTPChallengeOnly(t *testing.T) {
	t.Parallel()

	config := testGatewayConfig()
	config.Sites[0].TLS.Ready = false
	bundle, err := (Renderer{}).Render(config)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	host := string(bundleFile(t, bundle, bundleHostPath(t, bundle, "billing.example.com")))
	if strings.Contains(host, "listen 443 ssl") {
		t.Fatalf("automatic TLS must not activate before a certificate exists\n%s", host)
	}
	if !strings.Contains(host, ".well-known/acme-challenge") {
		t.Fatalf("bootstrap config must expose the ACME challenge\n%s", host)
	}
}

func TestRejectsDuplicateDomain(t *testing.T) {
	t.Parallel()

	config := testGatewayConfig()
	config.Sites = append(config.Sites, model.Site{
		ID: "duplicate", Name: "Duplicate", Domains: []string{"billing.example.com"},
	})
	if _, err := (Renderer{}).Render(config); err == nil {
		t.Fatal("expected duplicate domain to fail validation")
	}
}

func TestRenderEmptySitesCreatesPlaceholderHost(t *testing.T) {
	t.Parallel()

	config := model.GatewayConfig{
		Generation: 1,
		Revision:   "empty-rev",
		Sites:      nil,
	}
	bundle, err := (Renderer{}).Render(config)
	if err != nil {
		t.Fatalf("render empty config: %v", err)
	}
	paths := bundlePaths(bundle)
	if !slices.Contains(paths, "hosts/00-empty.conf") {
		t.Fatalf("expected placeholder host file, got paths: %v", paths)
	}
}

func testGatewayConfig() model.GatewayConfig {
	return model.GatewayConfig{
		Generation: 42,
		Revision:   "2026-11-21_03-30-42Z--rev-42--a81c7f",
		Sites: []model.Site{{
			ID:              "billing",
			Name:            "Billing",
			Enabled:         true,
			Domains:         []string{"billing.example.com", "api.example.com"},
			Upstreams:       []model.Upstream{{URL: "http://10.20.0.15:8080"}, {URL: "http://10.20.0.16:8080", Backup: true}},
			TLS:             model.TLSPolicy{Mode: "automatic", Email: "admin@example.com", Ready: true, RedirectHTTP: true},
			RateLimit:       model.RateLimitPolicy{Enabled: true, Requests: 60, PeriodSeconds: 60, Burst: 20, NoDelay: true},
			WAF:             model.WAFPolicy{Mode: "detect", ParanoiaLevel: 1},
			Geo:             model.GeoPolicy{Mode: "block", Countries: []string{"RU"}},
			SecurityHeaders: true,
		}},
	}
}

func bundlePaths(bundle Bundle) []string {
	paths := make([]string, 0, len(bundle.Files))
	for _, file := range bundle.Files {
		paths = append(paths, filepath.ToSlash(file.Path))
	}
	return paths
}

func bundleFile(t *testing.T, bundle Bundle, path string) []byte {
	t.Helper()
	for _, file := range bundle.Files {
		if filepath.ToSlash(file.Path) == path {
			return file.Contents
		}
	}
	t.Fatalf("bundle file %q not found", path)
	return nil
}

func bundleHostPath(t *testing.T, bundle Bundle, primaryDomain string) string {
	t.Helper()
	prefix := "hosts/" + primaryDomain + "--"
	for _, path := range bundlePaths(bundle) {
		if strings.HasPrefix(path, prefix) && strings.HasSuffix(path, ".conf") {
			return path
		}
	}
	t.Fatalf("host file for %q not found", primaryDomain)
	return ""
}
