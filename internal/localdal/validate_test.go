package localdal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateValid(t *testing.T) {
	root := t.TempDir()
	Init(root)

	// Create leader
	p, _ := CreateDal(root, "leader", "claude")
	// Set role to leader
	dalCue := filepath.Join(root, "leader", "dal.cue")
	pr, _ := ReadDalCue(dalCue, "leader")
	pr.Role = "leader"
	writeDalCue(dalCue, pr)

	// Create member
	CreateDal(root, "dev", "claude")

	// Create skill and add to dev
	CreateSkill(root, "code-review")
	AddSkillToDal(root, "dev", "code-review")

	errors := Validate(root)
	if len(errors) != 0 {
		t.Fatalf("expected 0 errors, got: %v", errors)
	}
	_ = p
}

func TestValidateNoLeader(t *testing.T) {
	root := t.TempDir()
	Init(root)
	CreateDal(root, "dev", "claude")

	errors := Validate(root)
	found := false
	for _, e := range errors {
		if e == "no leader dal found (exactly 1 required)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'no leader' error, got: %v", errors)
	}
}

func TestValidateMissingSkill(t *testing.T) {
	root := t.TempDir()
	Init(root)

	p, _ := CreateDal(root, "leader", "claude")
	dalCue := filepath.Join(root, "leader", "dal.cue")
	pr, _ := ReadDalCue(dalCue, "leader")
	pr.Role = "leader"
	pr.Skills = []string{"skills/nonexistent"}
	writeDalCue(dalCue, pr)

	errors := Validate(root)
	found := false
	for _, e := range errors {
		if e == `leader: skill "skills/nonexistent" not found` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing skill error, got: %v", errors)
	}
	_ = p
}

func TestValidateInvalidPlayer(t *testing.T) {
	root := t.TempDir()
	Init(root)

	dalDir := filepath.Join(root, "bad")
	os.MkdirAll(dalDir, 0755)
	os.WriteFile(filepath.Join(dalDir, "dal.cue"), []byte(`
uuid:    "test-uuid"
name:    "bad"
version: "1.0.0"
player:  "gpt4"
role:    "member"
skills:  []
hooks:   []
`), 0644)
	os.WriteFile(filepath.Join(dalDir, "charter.md"), []byte("# bad\n"), 0644)

	// Also need a leader
	CreateDal(root, "leader", "claude")
	lCue := filepath.Join(root, "leader", "dal.cue")
	lp, _ := ReadDalCue(lCue, "leader")
	lp.Role = "leader"
	writeDalCue(lCue, lp)

	errors := Validate(root)
	found := false
	for _, e := range errors {
		if e == `bad: invalid player "gpt4" (must be claude, codex, or gemini)` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected invalid player error, got: %v", errors)
	}
}
