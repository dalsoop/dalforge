// Package paths provides canonical directory paths for dalcenter runtime data.
//
// All dalcenter runtime data lives under DALCENTER_DATA_DIR (default: /var/lib/dalcenter):
//
//	$DALCENTER_DATA_DIR/
//	  soft-serve/           local git server data
//	  repos/                subtree sync clones
//	  registry-{hash}.json  container registry per daemon
//	  state/{repo-hash}/    git-external state per repo
package paths

import (
	"os"
	"path/filepath"
)

const defaultDataDir = "/var/lib/dalcenter"

// DataRootDir returns the root directory for all dalcenter runtime data.
// Reads DALCENTER_DATA_DIR; defaults to /var/lib/dalcenter.
func DataRootDir() string {
	if d := os.Getenv("DALCENTER_DATA_DIR"); d != "" {
		return d
	}
	return defaultDataDir
}

// StateBaseDir returns the base directory for git-external state.
func StateBaseDir() string {
	return filepath.Join(DataRootDir(), "state")
}

// ReposDir returns the directory for subtree sync repo clones.
func ReposDir() string {
	return filepath.Join(DataRootDir(), "repos")
}

// SoftServeDir returns the soft-serve data directory.
// SOFT_SERVE_DATA_PATH takes precedence if set.
func SoftServeDir() string {
	if p := os.Getenv("SOFT_SERVE_DATA_PATH"); p != "" {
		return p
	}
	return filepath.Join(DataRootDir(), "soft-serve")
}

// RegistryDir returns the directory for registry JSON files.
func RegistryDir() string {
	return DataRootDir()
}

const defaultConfigDir = "/etc/dalcenter"

// ConfigDir returns the directory for dalcenter env files.
// Reads DALCENTER_CONFIG_DIR; defaults to /etc/dalcenter.
func ConfigDir() string {
	if d := os.Getenv("DALCENTER_CONFIG_DIR"); d != "" {
		return d
	}
	return defaultConfigDir
}

// BinaryPath returns the path to the dalcenter binary.
// Uses os.Executable() to find the current binary, falling back to
// /usr/local/bin/dalcenter.
func BinaryPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "/usr/local/bin/dalcenter"
}
