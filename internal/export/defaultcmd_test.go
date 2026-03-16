package export

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCueFixture(t *testing.T, dir, content string) string {
	t.Helper()
	dalDir := filepath.Join(dir, ".dalfactory")
	os.MkdirAll(dalDir, 0755)
	path := filepath.Join(dalDir, "dal.cue")
	os.WriteFile(path, []byte(content), 0644)
	return dir
}

func TestDefaultCmdFromBuildEntry(t *testing.T) {
	dir := t.TempDir()
	writeCueFixture(t, dir, `
schema_version: "1.0.0"
dal: { id: "DAL:CLI:a1b2c3d4", name: "test", version: "0.1.0", category: "CLI" }
description: "test"
templates: default: {
    schema_version: "1.0.0"
    name: "default"
    description: "t"
    container: { base: "ubuntu:24.04", packages: [], agents: {} }
    permissions: { filesystem: [], network: false }
    compatibility: { os: ["linux"], arch: ["amd64"] }
    build: { language: "shell", entry: "bin/my-tool", output: "bin/my-tool" }
    health_check: { command: "echo ok" }
    exports: claude: { skills: [] }
}`)
	plan, err := LoadPlan(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if plan.DefaultCmd != "bin/my-tool" {
		t.Fatalf("expected bin/my-tool, got %q", plan.DefaultCmd)
	}
}

func TestAgentPackageFallback(t *testing.T) {
	dir := t.TempDir()
	// Create executable at the expected path
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0755)
	os.WriteFile(filepath.Join(binDir, "agent-bin"), []byte("#!/bin/sh\n"), 0755)

	writeCueFixture(t, dir, `
schema_version: "1.0.0"
dal: { id: "DAL:CLI:a1b2c3d4", name: "test", version: "0.1.0", category: "CLI" }
description: "test"
templates: default: {
    schema_version: "1.0.0"
    name: "default"
    description: "t"
    container: {
        base: "ubuntu:24.04"
        packages: []
        agents: {
            claude: { type: "claude_sdk", package: "bin/agent-bin" }
        }
    }
    permissions: { filesystem: [], network: false }
    compatibility: { os: ["linux"], arch: ["amd64"] }
    build: { language: "shell", entry: "bin/entry", output: "bin/entry" }
    health_check: { command: "echo ok" }
    exports: claude: { skills: [] }
}`)
	plan, err := LoadPlan(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// build.entry takes priority
	if plan.DefaultCmd != "bin/entry" {
		t.Fatalf("expected build.entry priority, got %q", plan.DefaultCmd)
	}
	// AgentPackages should be parsed
	if len(plan.AgentPackages) != 1 || plan.AgentPackages[0] != "bin/agent-bin" {
		t.Fatalf("expected [bin/agent-bin], got %v", plan.AgentPackages)
	}
}

func TestNoDefaultCmdOrAgents(t *testing.T) {
	dir := t.TempDir()
	writeCueFixture(t, dir, `
schema_version: "1.0.0"
dal: { id: "DAL:CLI:a1b2c3d4", name: "test", version: "0.1.0", category: "CLI" }
description: "test"
templates: default: {
    schema_version: "1.0.0"
    name: "default"
    description: "t"
    container: { base: "ubuntu:24.04", packages: [], agents: {} }
    permissions: { filesystem: [], network: false }
    compatibility: { os: ["linux"], arch: ["amd64"] }
    build: { language: "shell", entry: "bin/x", output: "bin/x" }
    health_check: { command: "echo ok" }
    exports: claude: { skills: [] }
}`)
	plan, err := LoadPlan(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(plan.AgentPackages) != 0 {
		t.Fatalf("expected no agent packages, got %v", plan.AgentPackages)
	}
}
