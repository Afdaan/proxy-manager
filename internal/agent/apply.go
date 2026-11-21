package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/afdaan/proxy-manager/internal/model"
	"github.com/afdaan/proxy-manager/internal/nginx"
)

const revisionRetention = 10

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

type revisionManifest struct {
	Generation uint64 `json:"generation"`
	Revision   string `json:"revision"`
}

func (applier *Applier) Apply(ctx context.Context, config model.GatewayConfig) error {
	applier.mu.Lock()
	defer applier.mu.Unlock()

	if applier.Runner == nil {
		return errors.New("command runner is required")
	}
	if applier.StateDir == "" || applier.ActiveConfigPath == "" {
		return errors.New("state and active config paths are required")
	}

	revisionDir := filepath.Join(applier.StateDir, "revisions", config.Revision)
	configPath := filepath.Join(revisionDir, "nginx.conf")
	bundle, err := applier.Renderer.RenderAt(config, revisionDir)
	if err != nil {
		return err
	}

	previousTarget, err := readSymlinkIfExists(applier.ActiveConfigPath)
	if err != nil {
		return fmt.Errorf("read active revision: %w", err)
	}
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

	if err := writeRevisionBundle(revisionDir, config, bundle); err != nil {
		return fmt.Errorf("write revision: %w", err)
	}
	if err := replaceSymlink(applier.ActiveConfigPath, configPath); err != nil {
		return fmt.Errorf("activate candidate revision: %w", err)
	}

	if err := applier.runNginx(ctx, "-t"); err != nil {
		_ = restoreSymlink(applier.ActiveConfigPath, previousTarget)
		return fmt.Errorf("nginx validation failed: %w", err)
	}
	if err := applier.runNginx(ctx, "-s", "reload"); err != nil {
		_ = restoreSymlink(applier.ActiveConfigPath, previousTarget)
		_ = applier.runNginx(ctx, "-s", "reload")
		return fmt.Errorf("nginx reload failed; previous revision restored: %w", err)
	}
	if previousTarget != "" {
		if err := writeFileAtomic(filepath.Join(applier.StateDir, "previous"), []byte(previousTarget), 0o600); err != nil {
			slog.Warn("active revision applied but previous revision pointer could not be recorded", "error", err)
		}
	}
	if err := applier.pruneRevisions(configPath, previousTarget); err != nil {
		slog.Warn("active revision applied but old revisions could not be pruned", "error", err)
	}
	return nil
}

func (applier *Applier) Rollback(ctx context.Context) error {
	applier.mu.Lock()
	defer applier.mu.Unlock()

	previousBytes, err := os.ReadFile(filepath.Join(applier.StateDir, "previous"))
	if err != nil {
		return fmt.Errorf("read previous revision: %w", err)
	}
	previousTarget := strings.TrimSpace(string(previousBytes))
	if _, err := os.Stat(previousTarget); err != nil {
		return fmt.Errorf("previous revision is unavailable: %w", err)
	}
	currentTarget, err := os.Readlink(applier.ActiveConfigPath)
	if err != nil {
		return fmt.Errorf("read current revision: %w", err)
	}
	if err := replaceSymlink(applier.ActiveConfigPath, previousTarget); err != nil {
		return fmt.Errorf("activate previous revision: %w", err)
	}

	if err := applier.runNginx(ctx, "-t"); err != nil {
		_ = restoreSymlink(applier.ActiveConfigPath, currentTarget)
		return fmt.Errorf("rollback validation failed: %w", err)
	}
	if err := applier.runNginx(ctx, "-s", "reload"); err != nil {
		_ = restoreSymlink(applier.ActiveConfigPath, currentTarget)
		_ = applier.runNginx(ctx, "-s", "reload")
		return fmt.Errorf("rollback reload failed: %w", err)
	}
	return writeFileAtomic(filepath.Join(applier.StateDir, "previous"), []byte(currentTarget), 0o600)
}

func (applier *Applier) runNginx(ctx context.Context, arguments ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return applier.Runner.Run(commandCtx, applier.nginxBinary(), arguments...)
}

func (applier *Applier) nginxBinary() string {
	if applier.NginxBinary == "" {
		return "/usr/sbin/nginx"
	}
	return applier.NginxBinary
}

func writeRevisionBundle(revisionDir string, config model.GatewayConfig, bundle nginx.Bundle) error {
	if existing, err := readRevisionManifest(revisionDir); err == nil {
		if existing.Generation == config.Generation && existing.Revision == config.Revision {
			return nil
		}
		return errors.New("immutable revision directory already belongs to another deployment")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	revisionsDir := filepath.Dir(revisionDir)
	if err := os.MkdirAll(revisionsDir, 0o750); err != nil {
		return err
	}
	temporaryDir, err := os.MkdirTemp(revisionsDir, ".revision-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporaryDir)
	if err := os.Chmod(temporaryDir, 0o750); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(bundle.Files))
	for _, file := range bundle.Files {
		relativePath, err := safeRelativePath(file.Path)
		if err != nil {
			return err
		}
		if _, exists := seen[relativePath]; exists {
			return fmt.Errorf("duplicate bundle path %q", relativePath)
		}
		seen[relativePath] = struct{}{}
		if err := writeFileAtomic(filepath.Join(temporaryDir, relativePath), file.Contents, 0o640); err != nil {
			return err
		}
	}
	if _, exists := seen["nginx.conf"]; !exists {
		return errors.New("bundle does not contain nginx.conf")
	}
	if _, exists := seen["manifest.json"]; !exists {
		return errors.New("bundle does not contain manifest.json")
	}
	return os.Rename(temporaryDir, revisionDir)
}

func safeRelativePath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if path == "" || filepath.IsAbs(cleaned) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe bundle path %q", path)
	}
	return cleaned, nil
}

func readSymlinkIfExists(path string) (string, error) {
	target, err := os.Readlink(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		information, statErr := os.Lstat(path)
		if statErr == nil && information.Mode().IsRegular() && information.Size() == 0 {
			return "", nil
		}
	}
	return target, err
}

func generationFromTarget(target string) (uint64, error) {
	if target == "" {
		return 0, nil
	}
	manifest, err := readRevisionManifest(filepath.Dir(target))
	if err != nil {
		return 0, err
	}
	return manifest.Generation, nil
}

func readRevisionManifest(revisionDir string) (revisionManifest, error) {
	contents, err := os.ReadFile(filepath.Join(revisionDir, "manifest.json"))
	if err != nil {
		return revisionManifest{}, err
	}
	var manifest revisionManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return revisionManifest{}, fmt.Errorf("decode revision manifest: %w", err)
	}
	if manifest.Generation == 0 || manifest.Revision == "" {
		return revisionManifest{}, errors.New("revision manifest is incomplete")
	}
	return manifest, nil
}

func (applier *Applier) pruneRevisions(activeTarget, previousTarget string) error {
	revisionsDir := filepath.Join(applier.StateDir, "revisions")
	entries, err := os.ReadDir(revisionsDir)
	if err != nil {
		return err
	}
	type revisionEntry struct {
		name    string
		modTime time.Time
	}
	revisions := make([]revisionEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		revisions = append(revisions, revisionEntry{name: entry.Name(), modTime: info.ModTime()})
	}
	slices.SortFunc(revisions, func(left, right revisionEntry) int {
		if compared := right.modTime.Compare(left.modTime); compared != 0 {
			return compared
		}
		return strings.Compare(right.name, left.name)
	})

	protected := map[string]struct{}{
		filepath.Dir(activeTarget):   {},
		filepath.Dir(previousTarget): {},
	}
	kept := 0
	for _, revision := range revisions {
		path := filepath.Join(revisionsDir, revision.name)
		if _, keep := protected[path]; keep || kept < revisionRetention {
			kept++
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
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
