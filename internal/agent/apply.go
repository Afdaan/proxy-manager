package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/afdaan/proxy-manager/internal/model"
	"github.com/afdaan/proxy-manager/internal/nginx"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) error
}

type Applier struct {
	Renderer         nginx.Renderer
	Runner           CommandRunner
	StateDir         string
	ActiveConfigPath string
	NginxBinary      string
	mu               sync.Mutex
}

func (applier *Applier) Apply(ctx context.Context, config model.GatewayConfig) error {
	applier.mu.Lock()
	defer applier.mu.Unlock()

	rendered, err := applier.Renderer.Render(config)
	if err != nil {
		return err
	}
	if applier.Runner == nil {
		return errors.New("command runner is required")
	}
	if applier.StateDir == "" || applier.ActiveConfigPath == "" {
		return errors.New("state and active config paths are required")
	}

	revisionName := fmt.Sprintf("%020d-%s", config.Generation, config.Revision)
	revisionDir := filepath.Join(applier.StateDir, "revisions", revisionName)
	configPath := filepath.Join(revisionDir, "nginx.conf")
	previousTarget, _ := os.Readlink(applier.ActiveConfigPath)
	currentGeneration, err := generationFromTarget(previousTarget)
	if err != nil {
		return fmt.Errorf("read active generation: %w", err)
	}
	if config.Generation < currentGeneration {
		return fmt.Errorf("stale generation %d; active generation is %d", config.Generation, currentGeneration)
	}
	if config.Generation == currentGeneration {
		if previousTarget == configPath {
			return nil
		}
		return fmt.Errorf("generation %d already belongs to another revision", config.Generation)
	}

	if err := os.MkdirAll(revisionDir, 0o750); err != nil {
		return fmt.Errorf("create revision directory: %w", err)
	}
	if err := writeFileAtomic(configPath, rendered, 0o640); err != nil {
		return fmt.Errorf("write revision: %w", err)
	}

	if err := replaceSymlink(applier.ActiveConfigPath, configPath); err != nil {
		return fmt.Errorf("activate candidate revision: %w", err)
	}

	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := applier.Runner.Run(commandCtx, applier.nginxBinary(), "-t"); err != nil {
		_ = restoreSymlink(applier.ActiveConfigPath, previousTarget)
		return fmt.Errorf("nginx validation failed: %w", err)
	}
	if err := applier.Runner.Run(commandCtx, applier.nginxBinary(), "-s", "reload"); err != nil {
		_ = restoreSymlink(applier.ActiveConfigPath, previousTarget)
		_ = applier.Runner.Run(commandCtx, applier.nginxBinary(), "-s", "reload")
		return fmt.Errorf("nginx reload failed; previous revision restored: %w", err)
	}
	if previousTarget != "" {
		if err := writeFileAtomic(filepath.Join(applier.StateDir, "previous"), []byte(previousTarget), 0o600); err != nil {
			return fmt.Errorf("record previous revision: %w", err)
		}
	}
	return nil
}

func generationFromTarget(target string) (uint64, error) {
	if target == "" {
		return 0, nil
	}
	directoryName := filepath.Base(filepath.Dir(target))
	generationText, _, found := strings.Cut(directoryName, "-")
	if !found {
		return 0, errors.New("active revision directory has no generation prefix")
	}
	generation, err := strconv.ParseUint(generationText, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse generation %q: %w", generationText, err)
	}
	return generation, nil
}

func (applier *Applier) Rollback(ctx context.Context) error {
	applier.mu.Lock()
	defer applier.mu.Unlock()

	previousBytes, err := os.ReadFile(filepath.Join(applier.StateDir, "previous"))
	if err != nil {
		return fmt.Errorf("read previous revision: %w", err)
	}
	previousTarget := string(previousBytes)
	currentTarget, err := os.Readlink(applier.ActiveConfigPath)
	if err != nil {
		return fmt.Errorf("read current revision: %w", err)
	}
	if err := replaceSymlink(applier.ActiveConfigPath, previousTarget); err != nil {
		return fmt.Errorf("activate previous revision: %w", err)
	}

	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := applier.Runner.Run(commandCtx, applier.nginxBinary(), "-t"); err != nil {
		_ = restoreSymlink(applier.ActiveConfigPath, currentTarget)
		return fmt.Errorf("rollback validation failed: %w", err)
	}
	if err := applier.Runner.Run(commandCtx, applier.nginxBinary(), "-s", "reload"); err != nil {
		_ = restoreSymlink(applier.ActiveConfigPath, currentTarget)
		_ = applier.Runner.Run(commandCtx, applier.nginxBinary(), "-s", "reload")
		return fmt.Errorf("rollback reload failed: %w", err)
	}
	return writeFileAtomic(filepath.Join(applier.StateDir, "previous"), []byte(currentTarget), 0o600)
}

func (applier *Applier) nginxBinary() string {
	if applier.NginxBinary == "" {
		return "/usr/sbin/nginx"
	}
	return applier.NginxBinary
}

func writeFileAtomic(path string, contents []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func replaceSymlink(path, target string) error {
	if target == "" {
		return errors.New("symlink target is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(path), ".active.tmp")
	_ = os.Remove(temporary)
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func restoreSymlink(path, target string) error {
	if target == "" {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return replaceSymlink(path, target)
}
