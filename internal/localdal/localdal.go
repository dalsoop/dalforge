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
	Player         string
	FallbackPlayer string // fallback player when primary fails (empty = auto-detect)
	PlayerVersion  string // e.g. "2.1.81" for claude, empty = latest
	Role          string // "leader" or "member"
	Model         string // optional model override (opus, sonnet, haiku)
	Skills     []string
	Hooks      []string
	FolderName string // directory name
	Path       string // absolute path to dal folder
	// Git config
	GitUser        string
	GitEmail       string
	GitHubToken    string // VeilKey ref or raw token
	GeminiAPIKey   string // VeilKey ref, env: ref, or raw key
	// Workspace mode
	Workspace string // "shared" (default, bind mount) or "clone" (git clone per dal)
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

	decisionsPath := filepath.Join(root, "decisions.md")
	if _, err := os.Stat(decisionsPath); err != nil {
		if err := os.WriteFile(decisionsPath, []byte(defaultDecisions), 0644); err != nil {
			return fmt.Errorf("write decisions.md: %w", err)
		}
	}

	archivePath := filepath.Join(root, "decisions-archive.md")
	if _, err := os.Stat(archivePath); err != nil {
		if err := os.WriteFile(archivePath, []byte(defaultDecisionsArchive), 0644); err != nil {
			return fmt.Errorf("write decisions-archive.md: %w", err)
		}
	}

	// .gitattributes in parent directory (service repo root)
	parentDir := filepath.Dir(root) // root is .dal/, parent is service repo
	gitattrsPath := filepath.Join(parentDir, ".gitattributes")
	if _, err := os.Stat(gitattrsPath); err != nil {
		if err := os.WriteFile(gitattrsPath, []byte(defaultGitattributes), 0644); err != nil {
			return fmt.Errorf("write .gitattributes: %w", err)
		}
	}

	// Auto-create scribe dal
	scribeDir := filepath.Join(root, "scribe")
	if _, err := os.Stat(scribeDir); err != nil {
		if err := os.MkdirAll(scribeDir, 0755); err != nil {
			return fmt.Errorf("create scribe dir: %w", err)
		}
		if err := os.WriteFile(filepath.Join(scribeDir, "dal.cue"), []byte(defaultScribeCue), 0644); err != nil {
			return fmt.Errorf("write scribe dal.cue: %w", err)
		}
		if err := os.WriteFile(filepath.Join(scribeDir, "charter.md"), []byte(defaultScribeInstructions), 0644); err != nil {
			return fmt.Errorf("write scribe charter.md: %w", err)
		}
	}

	// Auto-create wisdom.md
	wisdomPath := filepath.Join(root, "wisdom.md")
	if _, err := os.Stat(wisdomPath); err != nil {
		if err := os.WriteFile(wisdomPath, []byte(defaultWisdom), 0644); err != nil {
			return fmt.Errorf("write wisdom.md: %w", err)
		}
	}

	// Auto-create operational skills
	opsSkills := map[string]string{
		"inbox-protocol":    defaultSkillInboxProtocol,
		"history-hygiene":   defaultSkillHistoryHygiene,
		"escalation":        defaultSkillEscalation,
		"pre-flight":        defaultSkillPreFlight,
		"git-workflow":      defaultSkillGitWorkflow,
		"reviewer-protocol": defaultSkillReviewerProtocol,
		"leader-protocol":   defaultSkillLeaderProtocol,
	}
	for name, content := range opsSkills {
		skillDir := filepath.Join(root, "skills", name)
		skillFile := filepath.Join(skillDir, "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			os.MkdirAll(skillDir, 0755)
			if err := os.WriteFile(skillFile, []byte(content), 0644); err != nil {
				return fmt.Errorf("write skill %s: %w", name, err)
			}
		}
	}

	return nil
}

const defaultDecisions = `# Decisions

팀 아키텍처 결정 로그. 모든 dal은 작업 전에 이 파일을 읽는다.
scribe dal이 inbox에서 승인된 제안을 병합한다. 직접 수정 금지.

## 포맷

### {날짜}: {주제}
**By:** {dal name}
**What:** {결정 내용}
**Why:** {이유}

---
`

const defaultDecisionsArchive = `# Decisions Archive

아카이브된 결정. 읽기 전용 참조.
`

const defaultGitattributes = `.dal/decisions.md merge=union
.dal/decisions-archive.md merge=union
.dal/*/history.md merge=union
.dal/wisdom.md merge=union
`

const defaultScribeCue = `uuid:           "scribe-auto"
name:           "scribe"
version:        "1.0.0"
player:         "claude"
model:          "haiku"
role:           "member"
skills:         []
hooks:          []
auto_task:      "1. /workspace/decisions/inbox/ 파일 → decisions.md 병합 (중복 제거 후 삭제). 2. 각 dal /workspace/history-buffer/ → .dal/{name}/history.md 병합. 3. /workspace/wisdom-inbox/ → wisdom.md 병합. 4. history.md 12KB 초과 시 Core Context 압축. 5. decisions.md 50KB 초과 시 30일+ 항목 archive. 6. 변경 시 git add + commit + push."
auto_interval:  "30m"
git: {
	user:         "dal-scribe"
	email:        "dal-scribe@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
`

const defaultScribeInstructions = `# Scribe — 문서 관리자

## Role
팀 공유 기억의 유일한 file writer + committer. 사용자에게 보이지 않는 백그라운드 dal.

## Responsibilities
1. /workspace/decisions/inbox/ → decisions.md 병합 (중복 제거: By + What 조합 기준)
2. /workspace/wisdom-inbox/ → wisdom.md 병합
3. /workspace/history-buffer/{name}.md → .dal/{name}/history.md 병합
4. history.md 12KB 초과 시 Core Context로 압축
5. decisions.md 50KB 초과 시 30일+ 항목 → decisions-archive.md
6. 변경 시 git add + commit + push

## Boundaries
I handle: inbox 병합, history 압축, 아카이빙, 자동 커밋
I don't handle: 코드, 리뷰, 테스트, 라우팅, Mattermost 대화

## Rules
- push 실패 시 재시도만. force push, reset 금지. 3회 실패 시 leader에게 claim.
- 병합 후 inbox 파일 삭제.
- history에는 최종 결과만. 중간 상태 금지.
`

const defaultWisdom = `# Wisdom

팀 공유 교훈. 모든 dal은 작업 전에 이 파일을 읽는다.

## Patterns

검증된 접근 방식.

## Anti-Patterns

피해야 할 것.
`

// CreateDal creates a new dal folder with dal.cue and charter.md.
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

	if err := os.WriteFile(filepath.Join(dalDir, "charter.md"), []byte("# "+name+"\n"), 0644); err != nil {
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
	if v := val.LookupPath(cue.ParsePath("fallback_player")); v.Exists() {
		p.FallbackPlayer, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("player_version")); v.Exists() {
		p.PlayerVersion, _ = v.String()
	}
	if v := val.LookupPath(cue.ParsePath("model")); v.Exists() {
		p.Model, _ = v.String()
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
	// Workspace mode
	if v := val.LookupPath(cue.ParsePath("workspace")); v.Exists() {
		p.Workspace, _ = v.String()
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

const defaultSkillInboxProtocol = "# Inbox Protocol\n\ndecisions.md, wisdom.md 직접 수정 금지. inbox에 드롭.\n"
const defaultSkillHistoryHygiene = "# History Hygiene\n\n최종 결과만 기록. 중간 시도 금지. 12KB 제한.\n"
const defaultSkillEscalation = "# Escalation\n\nreport: 완료 보고. claim: 진행 불가 에스컬레이션.\n"
const defaultSkillPreFlight = "# Pre-Flight\n\n작업 전 필수: now.md → decisions.md → wisdom.md → ps.\n"
const defaultSkillGitWorkflow = "# Git Workflow\n\nmain 직접 커밋 금지. 브랜치 → PR → 리뷰 → 머지.\n"
const defaultSkillReviewerProtocol = "# Reviewer Protocol\n\n작성자 ≠ 리뷰어. 리뷰어 본인 수정 금지.\n"
const defaultSkillLeaderProtocol = "# Leader Protocol\n\n나는 중개자. 직접 수정 안 함. 소환+읽기+판단+라우팅만.\nWrite/Edit/commit 금지. dalcli-leader assign으로 멤버에게 위임.\n"

const defaultSpec = `// dal.spec.cue — localdal schema

#Player: "claude" | "codex" | "gemini"
#Role:   "leader" | "member"

#DalProfile: {
	uuid!:           string & != ""
	name!:           string & != ""
	version!:        string
	player!:           #Player
	fallback_player?:  #Player
	role!:             #Role
	skills?:         [...string]
	hooks?:          [...string]
	model?:          string
	player_version?: string
	auto_task?:      string
	auto_interval?:  string
	workspace?:      string
	git?: {
		user?:         string
		email?:        string
		github_token?: string
	}
}
`
