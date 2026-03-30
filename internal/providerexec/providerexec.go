package providerexec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func binaryCandidates() map[string][]string {
	home, _ := os.UserHomeDir()
	localBin := filepath.Join(home, ".local", "bin")
	return map[string][]string{
		"claude": {
			"claude",
			filepath.Join(localBin, "claude"),
			"/usr/local/bin/claude",
			"/usr/bin/claude",
		},
		"codex": {
			"codex",
			filepath.Join(localBin, "codex"),
			"/usr/local/bin/codex",
			"/usr/bin/codex",
		},
	}
}

// Resolve returns an executable path for the named provider binary.
// It checks PATH first, then a small set of known install locations.
func Resolve(player string) (string, error) {
	candidates, ok := binaryCandidates()[player]
	if !ok {
		return "", fmt.Errorf("unknown provider %q", player)
	}
	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) {
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}

func Exists(player string) bool {
	_, err := Resolve(player)
	return err == nil
}
