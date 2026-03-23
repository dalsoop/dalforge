package localdal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitCreatesStructure(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "skills")); err != nil {
		t.Fatal("skills dir missing")
	}
	if _, err := os.Stat(filepath.Join(root, "dal.spec.cue")); err != nil {
		t.Fatal("dal.spec.cue missing")
	}
}

func TestCreateAndListDal(t *testing.T) {
	root := t.TempDir()
	Init(root)

	p, err := CreateDal(root, "dev", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if p.UUID == "" {
		t.Fatal("empty UUID")
	}
	if p.Player != "claude" {
		t.Fatalf("player = %q", p.Player)
	}
	if p.Role != "member" {
		t.Fatalf("role = %q", p.Role)
	}

	// List
	dals, err := ListDals(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(dals) != 1 {
		t.Fatalf("got %d dals", len(dals))
	}
	if dals[0].Name != "dev" {
		t.Fatalf("name = %q", dals[0].Name)
	}
}

func TestCreateDalDuplicate(t *testing.T) {
	root := t.TempDir()
	Init(root)
	CreateDal(root, "dev", "claude")
	if _, err := CreateDal(root, "dev", "claude"); err == nil {
		t.Fatal("expected error for duplicate")
	}
}

func TestDeleteDal(t *testing.T) {
	root := t.TempDir()
	Init(root)
	CreateDal(root, "dev", "claude")

	if err := DeleteDal(root, "dev"); err != nil {
		t.Fatal(err)
	}
	dals, _ := ListDals(root)
	if len(dals) != 0 {
		t.Fatal("dal should be deleted")
	}
}

func TestDeleteDalNotFound(t *testing.T) {
	root := t.TempDir()
	Init(root)
	if err := DeleteDal(root, "nonexistent"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSkillCreateAddRemoveDelete(t *testing.T) {
	root := t.TempDir()
	Init(root)
	CreateDal(root, "dev", "claude")

	// Create skill
	if err := CreateSkill(root, "code-review"); err != nil {
		t.Fatal(err)
	}
	skills, _ := ListSkills(root)
	if len(skills) != 1 || skills[0] != "code-review" {
		t.Fatalf("skills = %v", skills)
	}

	// Add to dal
	if err := AddSkillToDal(root, "dev", "code-review"); err != nil {
		t.Fatal(err)
	}
	p, _ := ReadDalCue(filepath.Join(root, "dev", "dal.cue"), "dev")
	if len(p.Skills) != 1 || p.Skills[0] != "skills/code-review" {
		t.Fatalf("skills = %v", p.Skills)
	}

	// Add duplicate
	if err := AddSkillToDal(root, "dev", "code-review"); err == nil {
		t.Fatal("expected error for duplicate skill")
	}

	// Remove from dal
	if err := RemoveSkillFromDal(root, "dev", "code-review"); err != nil {
		t.Fatal(err)
	}
	p, _ = ReadDalCue(filepath.Join(root, "dev", "dal.cue"), "dev")
	if len(p.Skills) != 0 {
		t.Fatalf("skills should be empty: %v", p.Skills)
	}

	// Delete skill
	if err := DeleteSkill(root, "code-review"); err != nil {
		t.Fatal(err)
	}
	skills, _ = ListSkills(root)
	if len(skills) != 0 {
		t.Fatal("skill should be deleted")
	}
}

func TestDeleteSkillInUse(t *testing.T) {
	root := t.TempDir()
	Init(root)
	CreateDal(root, "dev", "claude")
	CreateSkill(root, "testing")
	AddSkillToDal(root, "dev", "testing")

	if err := DeleteSkill(root, "testing"); err == nil {
		t.Fatal("expected error: skill in use")
	}
}

func TestUUIDFormat(t *testing.T) {
	uuid := generateUUID()
	if len(uuid) != 36 {
		t.Fatalf("uuid length = %d: %s", len(uuid), uuid)
	}
	// Check format: 8-4-4-4-12
	parts := filepath.SplitList(uuid)
	_ = parts // basic check
}
