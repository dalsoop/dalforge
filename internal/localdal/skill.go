package localdal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateSkill creates a new skill folder with SKILL.md.
func CreateSkill(root, name string) error {
	skillDir := filepath.Join(root, "skills", name)
	if _, err := os.Stat(skillDir); err == nil {
		return fmt.Errorf("skill %q already exists", name)
	}
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name+"\n"), 0644)
}

// DeleteSkill removes a skill folder. Returns error if any dal references it.
func DeleteSkill(root, name string) error {
	// Check if any dal uses this skill
	dals, err := ListDals(root)
	if err != nil {
		return err
	}
	skillRef := "skills/" + name
	var users []string
	for _, d := range dals {
		for _, s := range d.Skills {
			if s == skillRef {
				users = append(users, d.Name)
			}
		}
	}
	if len(users) > 0 {
		return fmt.Errorf("skill %q is used by: %s", name, strings.Join(users, ", "))
	}

	skillDir := filepath.Join(root, "skills", name)
	if _, err := os.Stat(skillDir); err != nil {
		return fmt.Errorf("skill %q not found", name)
	}
	return os.RemoveAll(skillDir)
}

// ListSkills returns all skill folder names.
func ListSkills(root string) ([]string, error) {
	skillsDir := filepath.Join(root, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// AddSkillToDal adds a skill reference to a dal's dal.cue.
func AddSkillToDal(root, dalName, skillName string) error {
	dalCue := filepath.Join(root, dalName, "dal.cue")
	p, err := ReadDalCue(dalCue, dalName)
	if err != nil {
		return fmt.Errorf("read dal %q: %w", dalName, err)
	}

	skillRef := "skills/" + skillName
	// Check skill exists
	if _, err := os.Stat(filepath.Join(root, skillRef)); err != nil {
		return fmt.Errorf("skill %q not found", skillName)
	}
	// Check not already added
	for _, s := range p.Skills {
		if s == skillRef {
			return fmt.Errorf("dal %q already has skill %q", dalName, skillName)
		}
	}

	p.Skills = append(p.Skills, skillRef)
	return writeDalCue(dalCue, p)
}

// RemoveSkillFromDal removes a skill reference from a dal's dal.cue.
func RemoveSkillFromDal(root, dalName, skillName string) error {
	dalCue := filepath.Join(root, dalName, "dal.cue")
	p, err := ReadDalCue(dalCue, dalName)
	if err != nil {
		return fmt.Errorf("read dal %q: %w", dalName, err)
	}

	skillRef := "skills/" + skillName
	found := false
	var updated []string
	for _, s := range p.Skills {
		if s == skillRef {
			found = true
			continue
		}
		updated = append(updated, s)
	}
	if !found {
		return fmt.Errorf("dal %q does not have skill %q", dalName, skillName)
	}

	p.Skills = updated
	return writeDalCue(dalCue, p)
}

func writeDalCue(path string, p *DalProfile) error {
	var skillLines, hookLines string
	if len(p.Skills) > 0 {
		parts := make([]string, len(p.Skills))
		for i, s := range p.Skills {
			parts[i] = fmt.Sprintf("\t%q,", s)
		}
		skillLines = "[\n" + strings.Join(parts, "\n") + "\n]"
	} else {
		skillLines = "[]"
	}
	if len(p.Hooks) > 0 {
		parts := make([]string, len(p.Hooks))
		for i, h := range p.Hooks {
			parts[i] = fmt.Sprintf("\t%q,", h)
		}
		hookLines = "[\n" + strings.Join(parts, "\n") + "\n]"
	} else {
		hookLines = "[]"
	}

	content := fmt.Sprintf(`uuid:    %q
name:    %q
version: %q
player:  %q
role:    %q
skills:  %s
hooks:   %s
`, p.UUID, p.Name, p.Version, p.Player, p.Role, skillLines, hookLines)

	return os.WriteFile(path, []byte(content), 0644)
}
