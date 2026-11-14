package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/agent-mcp/client"
	"github.com/pijalu/agent-mcp/internal/output"
)

// artifactRetention is how long a default (temp-dir) artifact directory from
// an earlier run may sit in the OS temp location before a later run removes
// it. The CLI cannot clean up its own artifacts — the calling harness reads
// them after the process exits — so retention is enforced by the next run's
// sweep instead of at exit.
const artifactRetention = 24 * time.Hour

// artifactDirPrefix names the temp directories the CLI creates for default
// (no --save-binaries) artifact storage; the sweep only ever touches dirs
// with this prefix.
const artifactDirPrefix = "agent-mcp-binaries-"

// binaryEnv carries the resolved binary policy and store for one invocation.
// Parse-only fields live in commandParams; this is the executable half.
type binaryEnv struct {
	policy client.BinaryPolicy
	store  client.BinaryStore
}

// prepareBinaries resolves --binaries-policy and, for the file policy, the
// artifact store (the --save-binaries directory, or a fresh temp dir after
// sweeping stale ones). It runs once per invocation, before any request is
// built.
func (env *clientEnv) prepareBinaries() error {
	policy, err := client.ParseBinaryPolicy(env.params.binariesPolicy)
	if err != nil {
		return err
	}
	behind := binaryEnv{policy: policy}
	if policy == client.BinariesFile {
		dir, err := env.artifactDir()
		if err != nil {
			return err
		}
		behind.store = &artifactStore{dir: dir}
	}
	env.binaries = behind
	return nil
}

// artifactDir resolves where file-policy artifacts go this run: the
// --save-binaries directory when given (created if missing), otherwise a
// fresh temp directory, after sweeping dirs left by long-gone runs.
func (env *clientEnv) artifactDir() (string, error) {
	if dir := env.params.saveBinaries; dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("%w: create --save-binaries dir %s: %v", output.ErrInternal, dir, err)
		}
		return dir, nil
	}
	sweepStaleArtifactDirs()
	dir, err := os.MkdirTemp("", artifactDirPrefix)
	if err != nil {
		return "", fmt.Errorf("%w: create artifact dir: %v", output.ErrInternal, err)
	}
	return dir, nil
}

// artifactStore writes one artifact per binary part into dir. It is the
// CLI's BinaryStore implementation: the library decides what happens to
// binary data, this type only knows how to put bytes on disk.
type artifactStore struct {
	dir string
	seq int
}

// Store writes the artifact and returns its path for the pointer marker.
func (s *artifactStore) Store(artifact client.BinaryArtifact) (string, error) {
	s.seq++
	name := fmt.Sprintf("part-%02d%s", s.seq, client.ExtensionFor(artifact.MimeType))
	path := filepath.Join(s.dir, name)
	if err := os.WriteFile(path, artifact.Data, 0o600); err != nil {
		return "", fmt.Errorf("write artifact %s: %w", name, err)
	}
	return path, nil
}

// sweepStaleArtifactDirs removes default artifact directories older than the
// retention window. Best-effort by design: a sweeper that fails must never
// break the call that triggered it, and it only ever touches dirs the CLI
// itself created (artifactDirPrefix).
func sweepStaleArtifactDirs() {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), artifactDirPrefix+"*"))
	if err != nil {
		return
	}
	deadline := time.Now().Add(-artifactRetention)
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || !info.IsDir() || info.ModTime().After(deadline) {
			continue
		}
		_ = os.RemoveAll(match)
	}
}

// dataURLSize returns the decoded byte length of a data: URL's payload.
func dataURLSize(dataURL string) int {
	_, payload, found := strings.Cut(dataURL, ",")
	if !found {
		return 0
	}
	size := len(payload) / 4 * 3
	return size - strings.Count(payload, "=")
}
