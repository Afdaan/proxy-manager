package nginx

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/afdaan/proxy-manager/internal/model"
)

const defaultRevisionsRoot = "/var/lib/proxy-manager/revisions"

type Renderer struct {
	ACMEWebroot   string
	RevisionsRoot string
}

type Bundle struct {
	Files []File
}

type File struct {
	Path     string
	Contents []byte
}

type manifest struct {
	Generation uint64   `json:"generation"`
	Revision   string   `json:"revision"`
	Files      []string `json:"files"`
}

type rootData struct {
	Revision    string
	GlobalsGlob string
	HostsGlob   string
}

type siteData struct {
	model.Site
	UpstreamName string
	DirectiveID  string
	UpstreamURL  string
	UpstreamTLS  bool
	TLSActive    bool
	Rate         string
	Certificate  string
	PrivateKey   string
	GeoDefault   int
	GeoEntries   []geoEntry
}

type geoEntry struct {
	Country string
	Denied  int
}

func (renderer Renderer) Render(config model.GatewayConfig) (Bundle, error) {
	revisionsRoot := renderer.RevisionsRoot
	if revisionsRoot == "" {
		revisionsRoot = defaultRevisionsRoot
	}
	return renderer.RenderAt(config, filepath.Join(revisionsRoot, config.Revision))
}

func (renderer Renderer) RenderAt(config model.GatewayConfig, revisionDir string) (Bundle, error) {
	if err := config.Validate(); err != nil {
		return Bundle{}, err
	}
	if !filepath.IsAbs(revisionDir) {
		return Bundle{}, errors.New("revision directory must be absolute")
	}
	if renderer.ACMEWebroot == "" {
		renderer.ACMEWebroot = "/var/lib/proxy-manager/acme"
	}

	sites := make([]siteData, 0, len(config.Sites))
	for _, site := range config.Sites {
		if !site.Enabled {
			continue
		}
		prepared, err := renderer.prepareSite(site)
		if err != nil {
			return Bundle{}, fmt.Errorf("prepare site %q: %w", site.ID, err)
		}
		sites = append(sites, prepared)
	}
	slices.SortFunc(sites, func(left, right siteData) int {
		return strings.Compare(left.Domains[0], right.Domains[0])
	})

	bundle := Bundle{}
	root, err := executeTemplate("root", rootData{
		Revision:    config.Revision,
		GlobalsGlob: filepath.Join(revisionDir, "globals", "*.conf"),
		HostsGlob:   filepath.Join(revisionDir, "hosts", "*.conf"),
	})
	if err != nil {
		return Bundle{}, err
	}
	bundle.Files = append(bundle.Files, File{Path: "nginx.conf", Contents: root})

	runtimeConfig, err := executeTemplate("runtime", nil)
	if err != nil {
		return Bundle{}, err
	}
	bundle.Files = append(bundle.Files, File{Path: "globals/00-runtime.conf", Contents: runtimeConfig})

	rateLimits, err := executeTemplate("rateLimits", sites)
	if err != nil {
		return Bundle{}, err
	}
	if len(bytes.TrimSpace(rateLimits)) > 0 {
		bundle.Files = append(bundle.Files, File{Path: "globals/10-rate-limit-zones.conf", Contents: rateLimits})
	}

	geoMaps, err := executeTemplate("geoMaps", sites)
	if err != nil {
		return Bundle{}, err
	}
	if len(bytes.TrimSpace(geoMaps)) > 0 {
		bundle.Files = append(bundle.Files, File{Path: "globals/20-geoip-maps.conf", Contents: geoMaps})
	}

	for _, site := range sites {
		contents, err := executeTemplate("host", struct {
			ACMEWebroot string
			siteData
		}{ACMEWebroot: renderer.ACMEWebroot, siteData: site})
		if err != nil {
			return Bundle{}, err
		}
		bundle.Files = append(bundle.Files, File{
			Path:     filepath.Join("hosts", hostFilename(site)),
			Contents: contents,
		})
	}
	if len(sites) == 0 {
		bundle.Files = append(bundle.Files, File{
			Path:     filepath.Join("hosts", "00-empty.conf"),
			Contents: []byte("# No active proxy hosts\n"),
		})
	}

	manifestFiles := make([]string, 0, len(bundle.Files)+1)
	for _, file := range bundle.Files {
		manifestFiles = append(manifestFiles, filepath.ToSlash(file.Path))
	}
	manifestFiles = append(manifestFiles, "manifest.json")
	manifestContents, err := json.MarshalIndent(manifest{
		Generation: config.Generation,
		Revision:   config.Revision,
		Files:      manifestFiles,
	}, "", "  ")
	if err != nil {
		return Bundle{}, fmt.Errorf("render manifest: %w", err)
	}
	manifestContents = append(manifestContents, '\n')
	bundle.Files = append(bundle.Files, File{Path: "manifest.json", Contents: manifestContents})

	return bundle, nil
}

func hostFilename(site siteData) string {
	digest := sha256.Sum256([]byte(site.ID))
	return fmt.Sprintf("%s--%x.conf", site.Domains[0], digest[:3])
}

func (renderer Renderer) prepareSite(site model.Site) (siteData, error) {
	parsed, err := url.Parse(site.Upstreams[0].URL)
	if err != nil {
		return siteData{}, err
	}
	upstreamName := "pm_" + strings.ReplaceAll(site.ID, "-", "_")
	prepared := siteData{
		Site:         site,
		UpstreamName: upstreamName,
		DirectiveID:  strings.ReplaceAll(site.ID, "-", "_"),
		UpstreamURL:  parsed.Scheme + "://" + upstreamName,
		UpstreamTLS:  parsed.Scheme == "https",
	}
	if site.RateLimit.Enabled {
		period := "r/s"
		if site.RateLimit.PeriodSeconds == 60 {
			period = "r/m"
		}
		prepared.Rate = strconv.Itoa(site.RateLimit.Requests) + period
	}
	if site.Geo.Mode == "allow" {
		prepared.GeoDefault = 1
		for _, country := range site.Geo.Countries {
			prepared.GeoEntries = append(prepared.GeoEntries, geoEntry{Country: country})
		}
	} else if site.Geo.Mode == "block" {
		for _, country := range site.Geo.Countries {
			prepared.GeoEntries = append(prepared.GeoEntries, geoEntry{Country: country, Denied: 1})
		}
	}
	if site.TLS.Mode == "automatic" {
		prepared.Certificate = "/etc/letsencrypt/live/" + site.Domains[0] + "/fullchain.pem"
		prepared.PrivateKey = "/etc/letsencrypt/live/" + site.Domains[0] + "/privkey.pem"
		prepared.TLSActive = site.TLS.Ready
	} else if site.TLS.Mode == "custom" {
		prepared.Certificate = site.TLS.CertificatePath
		prepared.PrivateKey = site.TLS.PrivateKeyPath
		prepared.TLSActive = true
	}
	return prepared, nil
}

func executeTemplate(name string, data any) ([]byte, error) {
	var output bytes.Buffer
	if err := configTemplate.ExecuteTemplate(&output, name, data); err != nil {
		return nil, fmt.Errorf("render nginx %s config: %w", name, err)
	}
	return output.Bytes(), nil
}

//go:embed config.tmpl
var configTemplateSource string

var configTemplate = template.Must(template.New("nginx").Funcs(template.FuncMap{
	"join": strings.Join,
	"upstreamAddress": func(rawURL string) string {
		parsed, _ := url.Parse(rawURL)
		host := parsed.Host
		if parsed.Port() == "" {
			port := "80"
			if parsed.Scheme == "https" {
				port = "443"
			}
			if strings.Contains(parsed.Hostname(), ":") {
				host = net.JoinHostPort(parsed.Hostname(), port)
			} else {
				host = parsed.Hostname() + ":" + port
			}
		}
		return host
	},
	"wafFile": func(mode string) string {
		if mode == "detect" {
			return "/etc/proxy-manager/waf/detection-only.conf"
		}
		return "/etc/proxy-manager/waf/blocking.conf"
	},
}).Parse(configTemplateSource))
