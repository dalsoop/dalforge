package localdal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_CreatesDecisionsArchive(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)

	if err := Init(root); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "decisions-archive.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("decisions-archive.md not created: %v", err)
	}
	if !strings.Contains(string(data), "Archive") {
		t.Error("unexpected content")
	}
}

func TestInit_CreatesGitattributes(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)

	if err := Init(root); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmp, ".gitattributes")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(".gitattributes not created: %v", err)
	}
	if !strings.Contains(string(data), "merge=union") {
		t.Error("missing merge=union")
	}
}

func TestInit_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)

	// First init
	if err := Init(root); err != nil {
		t.Fatal(err)
	}

	// Modify decisions.md
	dpath := filepath.Join(root, "decisions.md")
	os.WriteFile(dpath, []byte("custom content"), 0644)

	// Second init should not overwrite
	if err := Init(root); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(dpath)
	if string(data) != "custom content" {
		t.Error("Init() overwrote existing decisions.md")
	}
}

func TestInit_CreatesScribeDal(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)

	if err := Init(root); err != nil {
		t.Fatal(err)
	}

	cue := filepath.Join(root, "scribe", "dal.cue")
	if _, err := os.Stat(cue); err != nil {
		t.Fatal("scribe/dal.cue not created")
	}

	instr := filepath.Join(root, "scribe", "instructions.md")
	if _, err := os.Stat(instr); err != nil {
		t.Fatal("scribe/instructions.md not created")
	}

	data, _ := os.ReadFile(cue)
	if !strings.Contains(string(data), "scribe") {
		t.Error("dal.cue should contain scribe")
	}
}

func TestInit_CreatesWisdomMd(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)

	if err := Init(root); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, "wisdom.md"))
	if err != nil {
		t.Fatal("wisdom.md not created")
	}
	if !strings.Contains(string(data), "Wisdom") {
		t.Error("unexpected content")
	}
}

func TestInit_CreatesOpsSkills(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)

	if err := Init(root); err != nil {
		t.Fatal(err)
	}

	expected := []string{"inbox-protocol", "history-hygiene", "escalation", "pre-flight", "git-workflow", "reviewer-protocol"}
	for _, name := range expected {
		path := filepath.Join(root, "skills", name, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("skill %s not created", name)
		}
	}
}

func TestInit_OpsSkillsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)

	Init(root)

	// Modify one skill
	path := filepath.Join(root, "skills", "escalation", "SKILL.md")
	os.WriteFile(path, []byte("custom"), 0644)

	// Re-init should not overwrite
	Init(root)

	data, _ := os.ReadFile(path)
	if string(data) != "custom" {
		t.Error("Init() overwrote existing skill")
	}
}

func TestInit_DecisionsTemplate(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)

	if err := Init(root); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, "decisions.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "직접 수정 금지") {
		t.Error("template should contain '직접 수정 금지'")
	}
	if !strings.Contains(content, "포맷") {
		t.Error("template should contain format guide")
	}
}

func TestInit_FullStructure(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)

	if err := Init(root); err != nil {
		t.Fatal(err)
	}

	// All expected files
	expected := []string{
		"dal.spec.cue",
		"decisions.md",
		"decisions-archive.md",
		"wisdom.md",
		"scribe/dal.cue",
		"scribe/instructions.md",
		"skills/inbox-protocol/SKILL.md",
		"skills/history-hygiene/SKILL.md",
		"skills/escalation/SKILL.md",
		"skills/pre-flight/SKILL.md",
		"skills/git-workflow/SKILL.md",
		"skills/reviewer-protocol/SKILL.md",
	}
	for _, f := range expected {
		path := filepath.Join(root, f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing: %s", f)
		}
	}

	// .gitattributes in parent (service repo root)
	gitattrs := filepath.Join(tmp, ".gitattributes")
	if _, err := os.Stat(gitattrs); err != nil {
		t.Error("missing .gitattributes in parent dir")
	}
}

func TestInit_ScribeDalCueContent(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)
	Init(root)

	data, _ := os.ReadFile(filepath.Join(root, "scribe", "dal.cue"))
	content := string(data)

	checks := []string{"scribe", "haiku", "auto_task", "30m", "dal-scribe", "GITHUB_TOKEN"}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("scribe dal.cue missing: %q", check)
		}
	}
}

func TestInit_WisdomTemplate(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)
	Init(root)

	data, _ := os.ReadFile(filepath.Join(root, "wisdom.md"))
	content := string(data)

	if !strings.Contains(content, "Patterns") {
		t.Error("wisdom.md missing Patterns section")
	}
	if !strings.Contains(content, "Anti-Patterns") {
		t.Error("wisdom.md missing Anti-Patterns section")
	}
}

func TestInit_GitattributesContent(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, ".dal")
	os.MkdirAll(root, 0755)
	Init(root)

	data, _ := os.ReadFile(filepath.Join(tmp, ".gitattributes"))
	content := string(data)

	checks := []string{"decisions.md merge=union", "history.md merge=union", "wisdom.md merge=union"}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf(".gitattributes missing: %q", check)
		}
	}
}
