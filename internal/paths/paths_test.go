package paths

import (
	"path/filepath"
	"testing"
)

func TestDataRootDir_Default(t *testing.T) {
	t.Setenv("DALCENTER_DATA_DIR", "")
	if got := DataRootDir(); got != "/var/lib/dalcenter" {
		t.Fatalf("DataRootDir() = %q, want /var/lib/dalcenter", got)
	}
}

func TestDataRootDir_EnvOverride(t *testing.T) {
	t.Setenv("DALCENTER_DATA_DIR", "/opt/dal")
	if got := DataRootDir(); got != "/opt/dal" {
		t.Fatalf("DataRootDir() = %q, want /opt/dal", got)
	}
}

func TestStateBaseDir_DerivedFromDataDir(t *testing.T) {
	t.Setenv("DALCENTER_DATA_DIR", "/opt/dal")
	if got := StateBaseDir(); got != "/opt/dal/state" {
		t.Fatalf("StateBaseDir() = %q, want /opt/dal/state", got)
	}
}

func TestReposDir(t *testing.T) {
	t.Setenv("DALCENTER_DATA_DIR", "/opt/dal")
	want := filepath.Join("/opt/dal", "repos")
	if got := ReposDir(); got != want {
		t.Fatalf("ReposDir() = %q, want %q", got, want)
	}
}

func TestSoftServeDir_Default(t *testing.T) {
	t.Setenv("DALCENTER_DATA_DIR", "/opt/dal")
	t.Setenv("SOFT_SERVE_DATA_PATH", "")
	want := filepath.Join("/opt/dal", "soft-serve")
	if got := SoftServeDir(); got != want {
		t.Fatalf("SoftServeDir() = %q, want %q", got, want)
	}
}

func TestSoftServeDir_EnvOverride(t *testing.T) {
	t.Setenv("SOFT_SERVE_DATA_PATH", "/custom/ss")
	if got := SoftServeDir(); got != "/custom/ss" {
		t.Fatalf("SoftServeDir() = %q, want /custom/ss", got)
	}
}
