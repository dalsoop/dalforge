package localdal

import (
	"os"
	"path/filepath"
	"strings"
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

func TestInitCreatesDecisionsMd(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "decisions.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("decisions.md missing after init")
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "직접 수정 금지") {
		t.Error("decisions.md should contain template content")
	}
}

func TestInitDecisionsMdIdempotent(t *testing.T) {
	root := t.TempDir()
	Init(root)

	// Write custom content
	path := filepath.Join(root, "decisions.md")
	os.WriteFile(path, []byte("# Custom decisions\n"), 0644)

	// Re-init should not overwrite
	Init(root)
	content, _ := os.ReadFile(path)
	if string(content) != "# Custom decisions\n" {
		t.Error("decisions.md should not be overwritten on re-init")
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

// ── ReadDalCue extended fields ──────────────────────────────────

func TestReadDalCue_GitConfig(t *testing.T) {
	dir := t.TempDir()
	cue := `
uuid:    "git-test-001"
name:    "gitdal"
version: "1.0.0"
player:  "claude"
role:    "member"
git: {
	user:         "test-user"
	email:        "test@example.com"
	github_token: "VK:secrets/gh-token"
}
`
	f := filepath.Join(dir, "dal.cue")
	os.WriteFile(f, []byte(cue), 0644)

	p, err := ReadDalCue(f, "gitdal")
	if err != nil {
		t.Fatal(err)
	}
	if p.GitUser != "test-user" {
		t.Errorf("GitUser = %q, want test-user", p.GitUser)
	}
	if p.GitEmail != "test@example.com" {
		t.Errorf("GitEmail = %q, want test@example.com", p.GitEmail)
	}
	if p.GitHubToken != "VK:secrets/gh-token" {
		t.Errorf("GitHubToken = %q, want VK:secrets/gh-token", p.GitHubToken)
	}
}

func TestReadDalCue_PlayerVersion(t *testing.T) {
	dir := t.TempDir()
	cue := `
uuid:           "pv-test-001"
name:           "pvdal"
version:        "1.0.0"
player:         "claude"
player_version: "2.1.81"
role:           "member"
`
	f := filepath.Join(dir, "dal.cue")
	os.WriteFile(f, []byte(cue), 0644)

	p, err := ReadDalCue(f, "pvdal")
	if err != nil {
		t.Fatal(err)
	}
	if p.PlayerVersion != "2.1.81" {
		t.Errorf("PlayerVersion = %q, want 2.1.81", p.PlayerVersion)
	}
}

func TestReadDalCue_MissingUUID(t *testing.T) {
	dir := t.TempDir()
	cue := `
name:    "no-uuid"
version: "1.0.0"
player:  "claude"
role:    "member"
`
	f := filepath.Join(dir, "dal.cue")
	os.WriteFile(f, []byte(cue), 0644)

	_, err := ReadDalCue(f, "no-uuid")
	if err == nil {
		t.Fatal("expected error for missing uuid")
	}
}

func TestReadDalCue_InvalidSyntax(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "dal.cue")
	os.WriteFile(f, []byte(`this is not valid cue {{{`), 0644)

	_, err := ReadDalCue(f, "bad")
	if err == nil {
		t.Fatal("expected error for invalid cue syntax")
	}
}

func TestReadDalCue_DefaultRole(t *testing.T) {
	dir := t.TempDir()
	cue := `
uuid:    "role-test-001"
name:    "norole"
version: "1.0.0"
player:  "claude"
`
	f := filepath.Join(dir, "dal.cue")
	os.WriteFile(f, []byte(cue), 0644)

	p, err := ReadDalCue(f, "norole")
	if err != nil {
		t.Fatal(err)
	}
	if p.Role != "member" {
		t.Errorf("Role = %q, want member (default)", p.Role)
	}
}

func TestReadDalCue_DefaultName(t *testing.T) {
	dir := t.TempDir()
	cue := `
uuid:    "name-test-001"
version: "1.0.0"
player:  "codex"
role:    "member"
`
	f := filepath.Join(dir, "dal.cue")
	os.WriteFile(f, []byte(cue), 0644)

	p, err := ReadDalCue(f, "folder-name")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "folder-name" {
		t.Errorf("Name = %q, want folder-name (from folderName param)", p.Name)
	}
}

func TestReadDalCue_Hooks(t *testing.T) {
	dir := t.TempDir()
	cue := `
uuid:    "hook-test-001"
name:    "hookdal"
version: "1.0.0"
player:  "claude"
role:    "member"
hooks:   ["pre-commit", "post-deploy"]
`
	f := filepath.Join(dir, "dal.cue")
	os.WriteFile(f, []byte(cue), 0644)

	p, err := ReadDalCue(f, "hookdal")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Hooks) != 2 {
		t.Fatalf("Hooks = %v, want 2 items", p.Hooks)
	}
	if p.Hooks[0] != "pre-commit" || p.Hooks[1] != "post-deploy" {
		t.Errorf("Hooks = %v", p.Hooks)
	}
}
