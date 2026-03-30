package localdal

import (
	"fmt"
	"os"
	"path/filepath"
)

// Validate checks all dal.cue files in root for schema and reference errors.
// Returns a list of error strings. Empty list = valid.
func Validate(root string) []string {
	var errors []string

	if _, err := os.Stat(root); err != nil {
		return []string{fmt.Sprintf("localdal root not found: %s", root)}
	}

	tplRoot := ResolveTemplateRoot(root)

	dals, err := ListDals(root)
	if err != nil {
		return []string{fmt.Sprintf("list dals: %v", err)}
	}

	if len(dals) == 0 {
		errors = append(errors, "no dals found")
		return errors
	}

	// Check each dal
	leaderCount := 0
	for _, d := range dals {
		// Required fields
		if d.UUID == "" {
			errors = append(errors, fmt.Sprintf("%s: missing uuid", d.FolderName))
		}
		if d.Name == "" {
			errors = append(errors, fmt.Sprintf("%s: missing name", d.FolderName))
		}
		if d.Player == "" {
			errors = append(errors, fmt.Sprintf("%s: missing player", d.FolderName))
		}
		switch d.Player {
		case "claude", "codex", "gemini":
		default:
			if d.Player != "" {
				errors = append(errors, fmt.Sprintf("%s: invalid player %q (must be claude, codex, or gemini)", d.FolderName, d.Player))
			}
		}
		switch d.Role {
		case "leader", "member", "ops":
		default:
			errors = append(errors, fmt.Sprintf("%s: invalid role %q (must be leader, member, or ops)", d.FolderName, d.Role))
		}
		if d.Role == "leader" {
			leaderCount++
		}

		// Check skill references exist
		for _, skill := range d.Skills {
			skillPath := filepath.Join(tplRoot, skill)
			if _, err := os.Stat(skillPath); err != nil {
				errors = append(errors, fmt.Sprintf("%s: skill %q not found", d.FolderName, skill))
			}
		}

		// Check hook references exist
		for _, hook := range d.Hooks {
			hookPath := filepath.Join(tplRoot, hook)
			if _, err := os.Stat(hookPath); err != nil {
				errors = append(errors, fmt.Sprintf("%s: hook %q not found", d.FolderName, hook))
			}
		}

		// Check charter.md exists
		instrPath := filepath.Join(tplRoot, d.FolderName, "charter.md")
		if _, err := os.Stat(instrPath); err != nil {
			errors = append(errors, fmt.Sprintf("%s: charter.md not found", d.FolderName))
		}
	}

	// At most one leader (ops-only teams don't require a leader)
	if leaderCount > 1 {
		errors = append(errors, fmt.Sprintf("found %d leaders (at most 1 allowed)", leaderCount))
	}

	return errors
}
