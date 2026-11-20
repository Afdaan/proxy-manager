package agent

import (
	"context"
	"errors"
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

func TestApplyIsAtomicAndIdempotent(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	runner := &recordingRunner{}
	applier := newTestApplier(temporaryDirectory, runner)
	first := testConfig(1, "first")

	if err := applier.Apply(context.Background(), first); err != nil {
		t.Fatalf("apply first revision: %v", err)
	}
	activeTarget, err := os.Readlink(applier.ActiveConfigPath)
	if err != nil {
		t.Fatalf("read active config: %v", err)
	}
	if !strings.Contains(activeTarget, "00000000000000000001-first") {
		t.Fatalf("unexpected active target %q", activeTarget)
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

	conflict := testConfig(1, "conflict")
	if err := applier.Apply(context.Background(), conflict); err == nil {
		t.Fatal("expected same generation with another revision to fail")
	}
}

func TestFailedValidationRestoresPreviousRevision(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	runner := &recordingRunner{}
	applier := newTestApplier(temporaryDirectory, runner)
	if err := applier.Apply(context.Background(), testConfig(1, "stable")); err != nil {
		t.Fatalf("apply stable revision: %v", err)
	}
	stableTarget, _ := os.Readlink(applier.ActiveConfigPath)

	runner.failTest = true
	if err := applier.Apply(context.Background(), testConfig(2, "broken")); err == nil {
		t.Fatal("expected validation failure")
	}
	activeTarget, _ := os.Readlink(applier.ActiveConfigPath)
	if activeTarget != stableTarget {
		t.Fatalf("expected %q after failed apply, got %q", stableTarget, activeTarget)
	}
}

func TestRollbackSwapsCurrentAndPrevious(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	runner := &recordingRunner{}
	applier := newTestApplier(temporaryDirectory, runner)
	if err := applier.Apply(context.Background(), testConfig(1, "first")); err != nil {
		t.Fatalf("apply first revision: %v", err)
	}
	if err := applier.Apply(context.Background(), testConfig(2, "second")); err != nil {
		t.Fatalf("apply second revision: %v", err)
	}
	if err := applier.Rollback(context.Background()); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	activeTarget, _ := os.Readlink(applier.ActiveConfigPath)
	if !strings.Contains(activeTarget, "00000000000000000001-first") {
		t.Fatalf("rollback selected unexpected target %q", activeTarget)
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
