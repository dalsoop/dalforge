package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDiscoverDalcenterServices_FromEnvFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)

	os.WriteFile(filepath.Join(dir, "team-a.env"), []byte("DALCENTER_PORT=11190\n"), 0644)
	os.WriteFile(filepath.Join(dir, "team-b.env"), []byte("DALCENTER_PORT=11191\n"), 0644)

	services := discoverDalcenterServices()
	sort.Strings(services)

	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d: %v", len(services), services)
	}
	if services[0] != "dalcenter@team-a" {
		t.Errorf("services[0] = %q, want %q", services[0], "dalcenter@team-a")
	}
	if services[1] != "dalcenter@team-b" {
		t.Errorf("services[1] = %q, want %q", services[1], "dalcenter@team-b")
	}
}

func TestDiscoverDalcenterServices_SkipsCommonEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)

	os.WriteFile(filepath.Join(dir, "common.env"), []byte("DALCENTER_HOST_IP=10.0.0.1\n"), 0644)
	os.WriteFile(filepath.Join(dir, "team-a.env"), []byte("DALCENTER_PORT=11190\n"), 0644)

	services := discoverDalcenterServices()
	if len(services) != 1 {
		t.Fatalf("expected 1 service (common.env skipped), got %d: %v", len(services), services)
	}
	if services[0] != "dalcenter@team-a" {
		t.Errorf("services[0] = %q, want %q", services[0], "dalcenter@team-a")
	}
}

func TestDiscoverDalcenterServices_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALCENTER_CONFIG_DIR", dir)

	services := discoverDalcenterServices()
	if len(services) != 0 {
		t.Errorf("expected 0 services for empty dir, got %d: %v", len(services), services)
	}
}

func TestDiscoverDalcenterServices_NonexistentDir(t *testing.T) {
	t.Setenv("DALCENTER_CONFIG_DIR", "/nonexistent/path")

	services := discoverDalcenterServices()
	if len(services) != 0 {
		t.Errorf("expected 0 services for nonexistent dir, got %d: %v", len(services), services)
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	content := []byte("test binary content")
	if err := os.WriteFile(src, content, 0755); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("copied content = %q, want %q", string(got), string(content))
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("copied file permissions = %o, want 0755", info.Mode().Perm())
	}
}

func TestCopyFile_SrcNotFound(t *testing.T) {
	dir := t.TempDir()
	err := copyFile(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dst"))
	if err == nil {
		t.Error("expected error for nonexistent source")
	}
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "new.bin")
	dst := filepath.Join(dir, "old.bin")

	os.WriteFile(src, []byte("new content"), 0755)
	os.WriteFile(dst, []byte("old content"), 0755)

	if err := replaceBinary(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new content" {
		t.Errorf("replaced content = %q, want %q", string(got), "new content")
	}

	// Source should be gone (renamed)
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source file should be removed after replace")
	}
}
