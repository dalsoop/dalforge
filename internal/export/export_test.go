package export

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPlanAndApply(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	skillDir := filepath.Join(repo, "skills", "demo-skill")
	manifestDir := filepath.Join(repo, ".dalfactory")
	manifestPath := filepath.Join(manifestDir, "dal.cue")
	claudeHome := filepath.Join(root, ".claude")

	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("demo"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`schema_version: "1.0.0"
dal: {
	id:       "DAL:CLI:848a4292"
	name:     "demo"
	version:  "0.1.0"
	category: "CLI"
}
templates: default: {
	schema_version: "1.0.0"
	name:           "default"
	container: {
		base:   "ubuntu:24.04"
		agents: {}
	}
	exports: claude: {
		skills: ["skills/demo-skill/SKILL.md"]
	}
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DALCENTER_CLAUDE_HOME", claudeHome)

	plan, err := LoadPlan(repo)
	if err != nil {
		t.Fatalf("LoadPlan returned error: %v", err)
	}
	if len(plan.Skills) != 1 {
		t.Fatalf("unexpected skill count: %d", len(plan.Skills))
	}

	if err := Apply(plan); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	link := filepath.Join(claudeHome, "skills", "demo-skill")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink returned error: %v", err)
	}
	if target != skillDir {
		t.Fatalf("unexpected symlink target: %s", target)
	}
}
