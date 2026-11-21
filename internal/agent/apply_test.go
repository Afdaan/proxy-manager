package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/afdaan/proxy-manager/internal/model"
	"github.com/afdaan/proxy-manager/internal/nginx"
)

type recordedCommand struct {
	name      string
	arguments []string
}

type recordingRunner struct {
	commands []recordedCommand
	failTest bool
}

func (runner *recordingRunner) Run(_ context.Context, name string, arguments ...string) error {
	runner.commands = append(runner.commands, recordedCommand{name: name, arguments: slices.Clone(arguments)})
	if runner.failTest && slices.Equal(arguments, []string{"-t"}) {
		return errors.New("invalid nginx config")
	}
	return nil
}

func TestApplyWritesBundleAtomicallyAndIsIdempotent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runner := &recordingRunner{}
	applier := newTestApplier(root, runner)
	first := testConfig(1, "2026-11-21_03-30-42Z--rev-1--first1")

	if err := applier.Apply(context.Background(), first); err != nil {
		t.Fatalf("apply first revision: %v", err)
	}
	activeTarget, err := os.Readlink(applier.ActiveConfigPath)
	if err != nil {
		t.Fatalf("read active config: %v", err)
	}
	if !strings.Contains(activeTarget, first.Revision) {
		t.Fatalf("unexpected active target %q", activeTarget)
	}
	for _, path := range []string{"nginx.conf", "manifest.json", "globals/00-runtime.conf"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(activeTarget), path)); err != nil {
			t.Errorf("revision missing %s: %v", path, err)
		}
	}
	hostFiles, err := filepath.Glob(filepath.Join(filepath.Dir(activeTarget), "hosts", "console.example.com--*.conf"))
	if err != nil || len(hostFiles) != 1 {
		t.Fatalf("expected one readable host file, got %v: %v", hostFiles, err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("expected test and reload commands, got %d", len(runner.commands))
	}

	if err := applier.Apply(context.Background(), first); err != nil {
		t.Fatalf("repeat same revision: %v", err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("idempotent apply ran commands again: %d", len(runner.commands))
	}

	conflict := testConfig(1, "2026-11-21_03-31-42Z--rev-1--other1")
	if err := applier.Apply(context.Background(), conflict); err == nil {
		t.Fatal("expected same generation with another revision to fail")
	}
}

func TestApplyReplacesEmptyBootstrapConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	applier := newTestApplier(root, &recordingRunner{})
	if err := os.MkdirAll(filepath.Dir(applier.ActiveConfigPath), 0o755); err != nil {
		t.Fatalf("create active config directory: %v", err)
	}
	if err := os.WriteFile(applier.ActiveConfigPath, nil, 0o640); err != nil {
		t.Fatalf("create bootstrap config: %v", err)
	}

	config := testConfig(1, "2026-11-21_03-30-42Z--rev-1--first1")
	if err := applier.Apply(context.Background(), config); err != nil {
		t.Fatalf("apply first revision: %v", err)
	}
	activeTarget, err := os.Readlink(applier.ActiveConfigPath)
	if err != nil {
		t.Fatalf("read active config symlink: %v", err)
	}
	if !strings.Contains(activeTarget, config.Revision) {
		t.Fatalf("unexpected active target %q", activeTarget)
	}
}

func TestFailedValidationRestoresPreviousRevision(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runner := &recordingRunner{}
	applier := newTestApplier(root, runner)
	if err := applier.Apply(context.Background(), testConfig(1, "2026-11-21_03-30-42Z--rev-1--stable")); err != nil {
		t.Fatalf("apply stable revision: %v", err)
	}
	stableTarget, _ := os.Readlink(applier.ActiveConfigPath)

	runner.failTest = true
	if err := applier.Apply(context.Background(), testConfig(2, "2026-11-21_03-31-42Z--rev-2--broken")); err == nil {
		t.Fatal("expected validation failure")
	}
	activeTarget, _ := os.Readlink(applier.ActiveConfigPath)
	if activeTarget != stableTarget {
		t.Fatalf("expected %q after failed apply, got %q", stableTarget, activeTarget)
	}
}

func TestRollbackSwapsCurrentAndPrevious(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runner := &recordingRunner{}
	applier := newTestApplier(root, runner)
	first := testConfig(1, "2026-11-21_03-30-42Z--rev-1--first1")
	second := testConfig(2, "2026-11-21_03-31-42Z--rev-2--second")
	if err := applier.Apply(context.Background(), first); err != nil {
		t.Fatalf("apply first revision: %v", err)
	}
	if err := applier.Apply(context.Background(), second); err != nil {
		t.Fatalf("apply second revision: %v", err)
	}
	if err := applier.Rollback(context.Background()); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	activeTarget, _ := os.Readlink(applier.ActiveConfigPath)
	if !strings.Contains(activeTarget, first.Revision) {
		t.Fatalf("rollback selected unexpected target %q", activeTarget)
	}
}

func TestRetentionKeepsTenLatestPlusPrevious(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	applier := newTestApplier(root, &recordingRunner{})
	for generation := uint64(1); generation <= 12; generation++ {
		revision := fmt.Sprintf("2026-11-21_03-%02d-42Z--rev-%d--abcdef", generation, generation)
		if err := applier.Apply(context.Background(), testConfig(generation, revision)); err != nil {
			t.Fatalf("apply generation %d: %v", generation, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(applier.StateDir, "revisions"))
	if err != nil {
		t.Fatalf("read revisions: %v", err)
	}
	if len(entries) != revisionRetention {
		t.Fatalf("expected %d retained revisions, got %d", revisionRetention, len(entries))
	}
}

func TestRejectsUnsafeBundlePath(t *testing.T) {
	t.Parallel()

	err := writeRevisionBundle(filepath.Join(t.TempDir(), "revision"), testConfig(1, "safe"), nginx.Bundle{Files: []nginx.File{
		{Path: "nginx.conf"},
		{Path: "manifest.json", Contents: []byte(`{"generation":1,"revision":"safe"}`)},
		{Path: "../escape.conf"},
	}})
	if err == nil {
		t.Fatal("expected unsafe bundle path to fail")
	}
}

func TestFailedFirstValidationKeepsEmptyConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runner := &recordingRunner{failTest: true}
	applier := newTestApplier(root, runner)
	if err := os.MkdirAll(filepath.Dir(applier.ActiveConfigPath), 0o755); err != nil {
		t.Fatalf("create active config directory: %v", err)
	}
	if err := os.WriteFile(applier.ActiveConfigPath, []byte("# Bootstrap\n"), 0o640); err != nil {
		t.Fatalf("create bootstrap config: %v", err)
	}

	config := testConfig(1, "2026-11-21_03-30-42Z--rev-1--failed")
	if err := applier.Apply(context.Background(), config); err == nil {
		t.Fatal("expected validation failure")
	}

	info, err := os.Stat(applier.ActiveConfigPath)
	if err != nil {
		t.Fatalf("active config was removed after failed validation: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("expected regular file, got %v", info.Mode())
	}
}

func newTestApplier(root string, runner CommandRunner) *Applier {
	return &Applier{
		Renderer:         nginx.Renderer{},
		Runner:           runner,
		StateDir:         filepath.Join(root, "state"),
		ActiveConfigPath: filepath.Join(root, "nginx", "active.conf"),
		NginxBinary:      "/usr/sbin/nginx",
	}
}

func testConfig(generation uint64, revision string) model.GatewayConfig {
	return model.GatewayConfig{
		Generation: generation,
		Revision:   revision,
		Sites: []model.Site{{
			ID:        "console",
			Name:      "Console",
			Enabled:   true,
			Domains:   []string{"console.example.com"},
			Upstreams: []model.Upstream{{URL: "http://10.0.0.10:8080"}},
		}},
	}
}
