package providerexec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrefersPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write temp binary: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)

	got, err := Resolve("claude")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got != bin {
		t.Fatalf("Resolve = %q, want %q", got, bin)
	}
}

func TestResolveUnknownProvider(t *testing.T) {
	if _, err := Resolve("unknown"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}
