package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"dalforge-hub/dalcenter/internal/validate"
)

// Plan captures a single repo export operation.
type Plan struct {
	RepoRoot string
	Manifest string
	Skills   []string
}

// LoadPlan reads one manifest and extracts Claude skill export paths.
func LoadPlan(path string) (*Plan, error) {
	manifestPath, err := validate.ResolveManifestPath(path)
	if err != nil {
		return nil, err
	}
	repoRoot := filepath.Dir(filepath.Dir(manifestPath))

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	ctx := cuecontext.New()
	val := ctx.CompileBytes(data)
	if err := val.Err(); err != nil {
		return nil, fmt.Errorf("compile cue: %w", err)
	}

	plan := &Plan{
		RepoRoot: repoRoot,
		Manifest: manifestPath,
	}

	for _, templateName := range templateNames(val) {
		path := cue.ParsePath("templates." + quoteLabel(templateName) + ".exports.claude.skills")
		v := val.LookupPath(path)
		if !v.Exists() {
			continue
		}
		iter, err := v.List()
		if err != nil {
			return nil, fmt.Errorf("read exports.claude.skills: %w", err)
		}
		for iter.Next() {
			s, err := iter.Value().String()
			if err != nil {
				return nil, fmt.Errorf("read skill path: %w", err)
			}
			plan.Skills = append(plan.Skills, s)
		}
		break
	}

	return plan, nil
}

// Apply creates Claude skill symlinks under the configured home.
func Apply(plan *Plan) error {
	if len(plan.Skills) == 0 {
		return nil
	}

	skillsRoot := filepath.Join(claudeHome(), "skills")
	if err := os.MkdirAll(skillsRoot, 0755); err != nil {
		return fmt.Errorf("create skills root: %w", err)
	}

	for _, rel := range plan.Skills {
		src := filepath.Join(plan.RepoRoot, filepath.Clean(rel))
		if !strings.HasPrefix(src, plan.RepoRoot+string(os.PathSeparator)) {
			return fmt.Errorf("skill path escapes repo root: %s", rel)
		}
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("stat skill file %s: %w", rel, err)
		}

		srcDir := filepath.Dir(src)
		name := filepath.Base(srcDir)
		dst := filepath.Join(skillsRoot, name)

		if err := replaceSymlink(dst, srcDir); err != nil {
			return fmt.Errorf("export skill %s: %w", rel, err)
		}
	}

	return nil
}

func claudeHome() string {
	if v := os.Getenv("DALCENTER_CLAUDE_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func replaceSymlink(dst, src string) error {
	if info, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		} else if err := os.Remove(dst); err != nil {
			return err
		}
	}
	return os.Symlink(src, dst)
}

func templateNames(v cue.Value) []string {
	t := v.LookupPath(cue.ParsePath("templates"))
	if !t.Exists() {
		return nil
	}
	fields, err := t.Fields()
	if err != nil {
		return nil
	}
	var names []string
	for fields.Next() {
		names = append(names, fields.Label())
	}
	return names
}

func quoteLabel(label string) string {
	if strings.ContainsAny(label, "-.") {
		return fmt.Sprintf("%q", label)
	}
	return label
}
