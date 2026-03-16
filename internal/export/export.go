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
	RepoRoot       string
	Manifest       string
	Exports        map[string][]string // runtime -> skill paths
	Hooks          map[string][]string // runtime -> hook paths
	HealthCheckCmd string
}

// SkillCount returns the number of declared exported skills across runtimes.
func SkillCount(plan *Plan) int {
	total := 0
	for _, skills := range plan.Exports {
		total += len(skills)
	}
	return total
}

// HookCount returns the number of declared exported hooks across runtimes.
func HookCount(plan *Plan) int {
	total := 0
	for _, hooks := range plan.Hooks {
		total += len(hooks)
	}
	return total
}

// LoadPlan reads one manifest and extracts runtime skill export paths.
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
		Exports:  map[string][]string{},
		Hooks:    map[string][]string{},
	}

	for _, templateName := range templateNames(val) {
		hcPath := cue.ParsePath("templates." + quoteLabel(templateName) + ".health_check.command")
		if hcVal := val.LookupPath(hcPath); hcVal.Exists() {
			if s, err := hcVal.String(); err == nil {
				plan.HealthCheckCmd = s
			}
		}

		exportsPath := cue.ParsePath("templates." + quoteLabel(templateName) + ".exports")
		exportsVal := val.LookupPath(exportsPath)
		if !exportsVal.Exists() {
			continue
		}
		runtimes, err := exportsVal.Fields()
		if err != nil {
			return nil, fmt.Errorf("read exports map: %w", err)
		}
		for runtimes.Next() {
			runtimeName := runtimes.Label()
			// Parse skills
			skillsVal := runtimes.Value().LookupPath(cue.ParsePath("skills"))
			if skillsVal.Exists() {
				iter, err := skillsVal.List()
				if err != nil {
					return nil, fmt.Errorf("read exports.%s.skills: %w", runtimeName, err)
				}
				for iter.Next() {
					s, err := iter.Value().String()
					if err != nil {
						return nil, fmt.Errorf("read skill path: %w", err)
					}
					plan.Exports[runtimeName] = append(plan.Exports[runtimeName], s)
				}
			}
			// Parse hooks
			hooksVal := runtimes.Value().LookupPath(cue.ParsePath("hooks"))
			if hooksVal.Exists() {
				iter, err := hooksVal.List()
				if err != nil {
					return nil, fmt.Errorf("read exports.%s.hooks: %w", runtimeName, err)
				}
				for iter.Next() {
					s, err := iter.Value().String()
					if err != nil {
						return nil, fmt.Errorf("read hook path: %w", err)
					}
					plan.Hooks[runtimeName] = append(plan.Hooks[runtimeName], s)
				}
			}
		}
		break
	}

	return plan, nil
}

// Apply creates runtime skill and hook symlinks under configured homes.
func Apply(plan *Plan) error {
	roots := map[string]string{}
	// Collect all runtimes from both exports and hooks
	for runtime := range plan.Exports {
		home, err := runtimeHome(runtime)
		if err != nil {
			return err
		}
		roots[runtime] = home
	}
	for runtime := range plan.Hooks {
		if _, ok := roots[runtime]; !ok {
			home, err := runtimeHome(runtime)
			if err != nil {
				return err
			}
			roots[runtime] = home
		}
	}
	return ApplyTo(plan, roots)
}

// ApplyTo creates runtime skill and hook symlinks under explicit runtime homes.
func ApplyTo(plan *Plan, runtimeHomes map[string]string) error {
	// Export skills
	for runtime, skills := range plan.Exports {
		home, ok := runtimeHomes[runtime]
		if !ok {
			continue
		}
		skillsRoot := filepath.Join(home, "skills")
		if err := os.MkdirAll(skillsRoot, 0755); err != nil {
			return fmt.Errorf("create skills root: %w", err)
		}

		for _, rel := range skills {
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
	}

	// Export hooks
	for runtime, hooks := range plan.Hooks {
		home, ok := runtimeHomes[runtime]
		if !ok {
			continue
		}
		hooksRoot := filepath.Join(home, "hooks")
		if err := os.MkdirAll(hooksRoot, 0755); err != nil {
			return fmt.Errorf("create hooks root: %w", err)
		}

		for _, rel := range hooks {
			src := filepath.Join(plan.RepoRoot, filepath.Clean(rel))
			if !strings.HasPrefix(src, plan.RepoRoot+string(os.PathSeparator)) {
				return fmt.Errorf("hook path escapes repo root: %s", rel)
			}
			if _, err := os.Stat(src); err != nil {
				return fmt.Errorf("stat hook file %s: %w", rel, err)
			}

			name := filepath.Base(src)
			dst := filepath.Join(hooksRoot, name)

			if err := replaceSymlink(dst, src); err != nil {
				return fmt.Errorf("export hook %s: %w", rel, err)
			}
		}
	}

	return nil
}

// Remove deletes runtime skill and hook symlinks that point at this plan's exports.
func Remove(plan *Plan) error {
	// Remove skills
	for runtime, skills := range plan.Exports {
		sr, err := skillsRoot(runtime)
		if err != nil {
			return err
		}
		for _, rel := range skills {
			src := filepath.Join(plan.RepoRoot, filepath.Clean(rel))
			if !strings.HasPrefix(src, plan.RepoRoot+string(os.PathSeparator)) {
				return fmt.Errorf("skill path escapes repo root: %s", rel)
			}
			srcDir := filepath.Dir(src)
			name := filepath.Base(srcDir)
			dst := filepath.Join(sr, name)
			if err := removeSymlink(dst, srcDir); err != nil {
				return fmt.Errorf("unexport skill %s: %w", rel, err)
			}
		}
	}

	// Remove hooks
	for runtime, hooks := range plan.Hooks {
		hr, err := hooksRoot(runtime)
		if err != nil {
			return err
		}
		for _, rel := range hooks {
			src := filepath.Join(plan.RepoRoot, filepath.Clean(rel))
			if !strings.HasPrefix(src, plan.RepoRoot+string(os.PathSeparator)) {
				return fmt.Errorf("hook path escapes repo root: %s", rel)
			}
			name := filepath.Base(src)
			dst := filepath.Join(hr, name)
			if err := removeSymlink(dst, src); err != nil {
				return fmt.Errorf("unexport hook %s: %w", rel, err)
			}
		}
	}

	return nil
}

func runtimeHome(runtime string) (string, error) {
	home, _ := os.UserHomeDir()
	switch runtime {
	case "claude":
		if v := os.Getenv("DALCENTER_CLAUDE_HOME"); v != "" {
			return v, nil
		}
		return filepath.Join(home, ".claude"), nil
	case "codex":
		if v := os.Getenv("DALCENTER_CODEX_HOME"); v != "" {
			return v, nil
		}
		return filepath.Join(home, ".codex"), nil
	default:
		return "", fmt.Errorf("unsupported runtime %q", runtime)
	}
}

func skillsRoot(runtime string) (string, error) {
	home, err := runtimeHome(runtime)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "skills"), nil
}

func hooksRoot(runtime string) (string, error) {
	home, err := runtimeHome(runtime)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "hooks"), nil
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

func removeSymlink(dst, src string) error {
	info, err := os.Lstat(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil
	}

	target, err := os.Readlink(dst)
	if err != nil {
		return err
	}
	if target != src {
		return nil
	}
	return os.Remove(dst)
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
