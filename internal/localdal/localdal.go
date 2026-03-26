package localdal

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// DalProfile represents a dal read from dal.cue.
type DalProfile struct {
	UUID       string
	Name       string
	Version    string
	Player        string
	PlayerVersion string // e.g. "2.1.81" for claude, empty = latest
	Role          string // "leader" or "member"
	Skills     []string
	Hooks      []string
	FolderName string // directory name
	Path       string // absolute path to dal folder
	// Git config
	GitUser        string
	GitEmail       string
	GitHubToken    string // VeilKey ref or raw token
	GeminiAPIKey   string // VeilKey ref, env: ref, or raw key
	// Auto task
	AutoTask     string // periodic task prompt (empty = disabled)
	AutoInterval string // interval like "1h", "30m" (default: disabled)
}

// Init initializes a localdal repository at the given path.
func Init(root string) error {
	dirs := []string{
		filepath.Join(root, "skills"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}

	specPath := filepath.Join(root, "dal.spec.cue")
	if _, err := os.Stat(specPath); err != nil {
		if err := os.WriteFile(specPath, []byte(defaultSpec), 0644); err != nil {
			return fmt.Errorf("write dal.spec.cue: %w", err)
		}
	}
	return nil
}

// CreateDal creates a new dal folder with dal.cue and instructions.md.
func CreateDal(root, name, player string) (*DalProfile, error) {
	dalDir := filepath.Join(root, name)
	if _, err := os.Stat(dalDir); err == nil {
		return nil, fmt.Errorf("dal %q already exists", name)
	}

	if err := os.MkdirAll(dalDir, 0755); err != nil {
		return nil, err
	}

	uuid := generateUUID()
	cueContent := fmt.Sprintf(`uuid:    %q
name:    %q
version: "0.1.0"
player:  %q
role:    "member"
skills:  []
hooks:   []
`, uuid, name, player)

	if err := os.WriteFile(filepath.Join(dalDir, "dal.cue"), []byte(cueContent), 0644); err != nil {
		return nil, err
	}

	if err := os.WriteFile(filepath.Join(dalDir, "instructions.md"), []byte("# "+name+"\n"), 0644); err != nil {
		return nil, err
	}

	return &DalProfile{
		UUID:       uuid,
		Name:       name,
		Version:    "0.1.0",
		Player:     player,
		Role:       "member",
		FolderName: name,
		Path:       dalDir,
	}, nil
}

// DeleteDal removes a dal folder.
func DeleteDal(root, name string) error {
	dalDir := filepath.Join(root, name)
	if _, err := os.Stat(filepath.Join(dalDir, "dal.cue")); err != nil {
		return fmt.Errorf("dal %q not found", name)
	}
	return os.RemoveAll(dalDir)
}

// ListDals scans the root for dal folders (containing dal.cue).
func ListDals(root string) ([]DalProfile, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}

	var dals []DalProfile
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dalCue := filepath.Join(root, entry.Name(), "dal.cue")
		if _, err := os.Stat(dalCue); err != nil {
			continue
		}
		p, err := ReadDalCue(dalCue, entry.Name())
		if err != nil {
			continue
		}
		p.Path = filepath.Join(root, entry.Name())
		dals = append(dals, *p)
	}
	return dals, nil
}

// ReadDalCue parses a dal.cue file.
func ReadDalCue(path, folderName string) (*DalProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	ctx := cuecontext.New()
	val := ctx.CompileBytes(data)
	if err := val.Err(); err != nil {
		return nil, fmt.Errorf("compile cue: %w", err)
	}

	p := &DalProfile{FolderName: folderName}

	if v := val.LookupPath(cue.ParsePath("uuid")); v.Exists() {
		p.UUID, _ = v.String()
	}
	if p.UUID == "" {
		return nil, fmt.Errorf("missing uuid")
	}
	if v := val.LookupPath(cue.ParsePath("name")); v.Exists() {
		p.Name, _ = v.String()
	}
	if p.Name == "" {
		p.Name = folderName
	}
	if v := val.LookupPath(cue.ParsePath("version")); v.Exists() {
		p.Version, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("player")); v.Exists() {
		p.Player, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("player_version")); v.Exists() {
		p.PlayerVersion, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("role")); v.Exists() {
		p.Role, _ = v.String()
	}
	if p.Role == "" {
		p.Role = "member"
	}
	if v := val.LookupPath(cue.ParsePath("skills")); v.Exists() {
		if iter, err := v.List(); err == nil {
			for iter.Next() {
				if s, err := iter.Value().String(); err == nil {
					p.Skills = append(p.Skills, s)
				}
			}
		}
	}
	if v := val.LookupPath(cue.ParsePath("hooks")); v.Exists() {
		if iter, err := v.List(); err == nil {
			for iter.Next() {
				if s, err := iter.Value().String(); err == nil {
					p.Hooks = append(p.Hooks, s)
				}
			}
		}
	}
	// Git config
	if v := val.LookupPath(cue.ParsePath("git.user")); v.Exists() {
		p.GitUser, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("git.email")); v.Exists() {
		p.GitEmail, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("git.github_token")); v.Exists() {
		p.GitHubToken, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("gemini_api_key")); v.Exists() {
		p.GeminiAPIKey, _ = v.String()
	}
	// Auto task
	if v := val.LookupPath(cue.ParsePath("auto_task")); v.Exists() {
		p.AutoTask, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("auto_interval")); v.Exists() {
		p.AutoInterval, _ = v.String()
	}
	return p, nil
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

const defaultSpec = `// dal.spec.cue — localdal schema

#Player: "claude" | "codex" | "gemini"
#Role:   "leader" | "member"

#DalProfile: {
	uuid!:    string & != ""
	name!:    string & != ""
	version!: string
	player!:  #Player
	role!:    #Role
	skills?:  [...string]
	hooks?:   [...string]
}
`
