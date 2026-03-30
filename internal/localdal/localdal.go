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

const (
	// TemplateDir is the subdirectory under .dal/ that holds dal blueprints.
	TemplateDir = "template"
	// IssueDir is the subdirectory under .dal/ that holds per-issue workspaces.
	IssueDir = "issue"
)

// TemplatePath returns the .dal/template/ path for a localdal root.
func TemplatePath(dalRoot string) string {
	return filepath.Join(dalRoot, TemplateDir)
}

// IssuePath returns the .dal/issue/{id}/ path for a localdal root.
func IssuePath(dalRoot string, issueID string) string {
	return filepath.Join(dalRoot, IssueDir, issueID)
}

// BranchConfig declares the base branch for issue-based branching.
// The actual branch name (issue-{N}/{dal-name}) is determined at wake time via --issue.
type BranchConfig struct {
	Base string // base branch to create from (default: "main")
}

// SetupConfig declares commands to run after branch checkout to make the workspace ready-to-code.
type SetupConfig struct {
	Packages []string // apt packages to install (e.g. ["protobuf-compiler"])
	Commands []string // shell commands in order (e.g. ["go mod download", "go build ./..."])
	Timeout  string   // max time for all setup commands (default: "5m")
}

// DalProfile represents a dal read from dal.cue.
type DalProfile struct {
	UUID           string
	Name           string
	Version        string
	Player         string
	FallbackPlayer string // fallback player when primary fails (empty = auto-detect)
	PlayerVersion  string // e.g. "2.1.81" for claude, empty = latest
	Role           string // "leader" or "member"
	ChannelOnly    bool   // disable DM polling; project channel + threads only
	Model          string // optional model override (opus, sonnet, haiku)
	Skills         []string
	Hooks          []string
	FolderName     string // directory name
	Path           string // absolute path to dal folder
	// Git config
	GitUser      string
	GitEmail     string
	GitHubToken  string // VeilKey ref or raw token
	GeminiAPIKey string // VeilKey ref, env: ref, or raw key
	// Workspace mode
	Workspace string // "shared" (default, bind mount) or "clone" (git clone per dal)
	// Auto task
	AutoTask     string // periodic task prompt (empty = disabled)
	AutoInterval string // interval like "1h", "30m" (default: disabled)
	// Branch config
	Branch BranchConfig
	// Setup config (ready-to-code environment)
	Setup SetupConfig
}

// Init initializes a localdal repository at the given path.
// Creates .dal/template/ and .dal/issue/ structure.
func Init(root string) error {
	// Create template/ and issue/ subdirectories
	tplRoot := TemplatePath(root)
	issueRoot := filepath.Join(root, IssueDir)
	for _, d := range []string{tplRoot, issueRoot} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}

	// Use template root for all dal content
	root = tplRoot

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

	// Auto-create dalops (operations — CCW-based orchestration)
	dalopsDir := filepath.Join(root, "dalops")
	if _, err := os.Stat(dalopsDir); err != nil {
		if err := os.MkdirAll(dalopsDir, 0755); err != nil {
			return fmt.Errorf("create dalops dir: %w", err)
		}
		dalopsUUID := generateUUID()
		dalopsCue := fmt.Sprintf(defaultDalopsCueTemplate, dalopsUUID)
		if err := os.WriteFile(filepath.Join(dalopsDir, "dal.cue"), []byte(dalopsCue), 0644); err != nil {
			return fmt.Errorf("write dalops dal.cue: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dalopsDir, "charter.md"), []byte(defaultDalopsCharter), 0644); err != nil {
			return fmt.Errorf("write dalops charter.md: %w", err)
		}
	}

	// Auto-create dal (document manager)
	dalDir := filepath.Join(root, "dal")
	if _, err := os.Stat(dalDir); err != nil {
		if err := os.MkdirAll(dalDir, 0755); err != nil {
			return fmt.Errorf("create dal dir: %w", err)
		}
		dalUUID := generateUUID()
		dalCue := fmt.Sprintf(defaultDalCueTemplate, dalUUID)
		if err := os.WriteFile(filepath.Join(dalDir, "dal.cue"), []byte(dalCue), 0644); err != nil {
			return fmt.Errorf("write dal dal.cue: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dalDir, "charter.md"), []byte(defaultDalCharter), 0644); err != nil {
			return fmt.Errorf("write dal charter.md: %w", err)
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

// CreateDal creates a new dal folder with dal.cue and charter.md under template/.
func CreateDal(root, name, player string) (*DalProfile, error) {
	tplRoot := ResolveTemplateRoot(root)
	dalDir := filepath.Join(tplRoot, name)
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

// PrepareIssue creates an issue workspace by copying dal templates for specified members.
// If the issue directory already exists, it is left unchanged.
func PrepareIssue(dalRoot, issueID string, members []string) (string, error) {
	issuePath := IssuePath(dalRoot, issueID)
	if _, err := os.Stat(issuePath); err == nil {
		return issuePath, nil // already exists
	}
	if err := os.MkdirAll(issuePath, 0755); err != nil {
		return "", fmt.Errorf("create issue dir: %w", err)
	}

	tplRoot := ResolveTemplateRoot(dalRoot)

	// Copy each member's dal.cue + charter.md from template
	for _, name := range members {
		srcDir := filepath.Join(tplRoot, name)
		if _, err := os.Stat(filepath.Join(srcDir, "dal.cue")); err != nil {
			continue // template not found, skip
		}
		dstDir := filepath.Join(issuePath, name)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return "", fmt.Errorf("create %s: %w", dstDir, err)
		}
		// Copy dal.cue
		if data, err := os.ReadFile(filepath.Join(srcDir, "dal.cue")); err == nil {
			os.WriteFile(filepath.Join(dstDir, "dal.cue"), data, 0644)
		}
		// Copy charter.md (instructions)
		if data, err := os.ReadFile(filepath.Join(srcDir, "charter.md")); err == nil {
			os.WriteFile(filepath.Join(dstDir, "charter.md"), data, 0644)
		}
	}

	// Create issue.md stub
	issueMD := fmt.Sprintf("# Issue %s\n\n## 목표\n\n## 범위\n", issueID)
	os.WriteFile(filepath.Join(issuePath, "issue.md"), []byte(issueMD), 0644)

	return issuePath, nil
}

// DeleteDal removes a dal folder from template/.
func DeleteDal(root, name string) error {
	tplRoot := ResolveTemplateRoot(root)
	dalDir := filepath.Join(tplRoot, name)
	if _, err := os.Stat(filepath.Join(dalDir, "dal.cue")); err != nil {
		return fmt.Errorf("dal %q not found", name)
	}
	return os.RemoveAll(dalDir)
}

// ResolveTemplateRoot returns the directory containing dal templates.
// If .dal/template/ exists, use it. Otherwise fall back to .dal/ (legacy).
func ResolveTemplateRoot(dalRoot string) string {
	tpl := TemplatePath(dalRoot)
	if info, err := os.Stat(tpl); err == nil && info.IsDir() {
		return tpl
	}
	return dalRoot
}

// ListDals scans the template root for dal folders (containing dal.cue).
func ListDals(root string) ([]DalProfile, error) {
	scanRoot := ResolveTemplateRoot(root)
	entries, err := os.ReadDir(scanRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", scanRoot, err)
	}

	var dals []DalProfile
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dalCue := filepath.Join(scanRoot, entry.Name(), "dal.cue")
		if _, err := os.Stat(dalCue); err != nil {
			continue
		}
		p, err := ReadDalCue(dalCue, entry.Name())
		if err != nil {
			continue
		}
		p.Path = filepath.Join(scanRoot, entry.Name())
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
	if v := val.LookupPath(cue.ParsePath("channel_only")); v.Exists() {
		p.ChannelOnly, _ = v.Bool()
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
	// Branch config
	if v := val.LookupPath(cue.ParsePath("branch.base")); v.Exists() {
		p.Branch.Base, _ = v.String()
	}
	if p.Branch.Base == "" {
		p.Branch.Base = "main"
	}
	// Setup config
	if v := val.LookupPath(cue.ParsePath("setup.packages")); v.Exists() {
		if iter, err := v.List(); err == nil {
			for iter.Next() {
				if s, err := iter.Value().String(); err == nil {
					p.Setup.Packages = append(p.Setup.Packages, s)
				}
			}
		}
	}
	if v := val.LookupPath(cue.ParsePath("setup.commands")); v.Exists() {
		if iter, err := v.List(); err == nil {
			for iter.Next() {
				if s, err := iter.Value().String(); err == nil {
					p.Setup.Commands = append(p.Setup.Commands, s)
				}
			}
		}
	}
	if v := val.LookupPath(cue.ParsePath("setup.timeout")); v.Exists() {
		p.Setup.Timeout, _ = v.String()
	}
	if p.Setup.Timeout == "" {
		p.Setup.Timeout = "5m"
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

// defaultLeaderCueTemplate has one %s placeholder for the UUID.
const defaultLeaderCueTemplate = `uuid:    %q
name:    "leader"
version: "1.0.0"
player:  "claude"
role:    "leader"
channel_only: true
skills:  ["skills/leader-protocol", "skills/inbox-protocol", "skills/pre-flight"]
hooks:   []
git: {
	user:         "dal-leader"
	email:        "dal-leader@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
`

const defaultLeaderCharter = `# Leader Dal

## Role
팀 리더. 작업을 분배하고 결과를 검토한다. 직접 코드를 수정하지 않는다.

## Tools
- dalcli-leader ps — 팀 상태 확인
- dalcli-leader wake/sleep <dal> — 멤버 관리
- dalcli-leader assign <dal> <task> — 작업 배정
- dalcli-leader logs <dal> — 멤버 로그 확인

## Workflow
1. 작업 요청 수신
2. 하위 작업으로 분해
3. 멤버에게 배정 (dalcli-leader assign)
4. 진행 상황 모니터링
5. 결과 검토 및 피드백
6. 완료 시 최종 PR 생성

## Rules
- main 직접 커밋 금지
- Write/Edit/commit 금지 — dalcli-leader assign으로 위임
- 리뷰 없이 머지 금지
`

// defaultDevCueTemplate has one %s placeholder for the UUID.
const defaultDevCueTemplate = `uuid:    %q
name:    "dev"
version: "1.0.0"
player:  "claude"
role:    "member"
channel_only: true
skills:  ["skills/git-workflow", "skills/pre-flight", "skills/inbox-protocol"]
hooks:   []
git: {
	user:         "dal-dev"
	email:        "dal-dev@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
`

const defaultDevCharter = `# Dev Dal

## Role
개발자. 코드를 작성하고 테스트한다.

## Tools
- dalcli status — 내 상태 확인
- dalcli ps — 팀 상태 확인
- dalcli report <message> — 리더에게 보고

## Workflow
1. 배정된 작업 확인
2. 브랜치 생성: git checkout -b feat/<task>
3. 구현 및 테스트
4. 커밋, 푸시, PR 생성
5. dalcli report로 완료 보고

## Rules
- main 직접 커밋 금지
- 테스트 작성 필수
- 작고 명확한 커밋
- 기존 코드 패턴 준수
`

// defaultDalopsCueTemplate has one %s placeholder for the UUID.
const defaultDalopsCueTemplate = `uuid:    %q
name:    "dalops"
version: "1.0.0"
player:  "claude"
role:    "ops"
channel_only: true
skills:  ["skills/git-workflow", "skills/pre-flight"]
hooks:   []
git: {
	user:         "dal-ops"
	email:        "dal-ops@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
`

const defaultDalopsCharter = `# dalops — 운영자

## Role
CCW 기반 오케스트레이터. 코드 구현, 리뷰, 테스트를 워크플로우로 실행한다.

## Tools
- ccw cli --tool codex --mode review — Codex 코드 리뷰
- ccw cli --tool codex --mode analysis — Codex 분석
- ccw cli --tool gemini — Gemini 분석
- quorum verify — 보안/시크릿/의존성 검사
- dalcli status / ps / report

## Workflows
- workflow-lite-plan — 단일 모듈 기능 구현
- workflow-tdd-plan — 테스트 주도 개발
- workflow-multi-cli-plan — 멀티 CLI 협업 분석/리뷰
- workflow-test-fix — 테스트 생성 및 수정 루프

## Process
1. 이슈/작업 수신
2. CCW 워크플로우 선택 및 실행
3. codex 리뷰 통과 확인
4. quorum verify (보안 검사)
5. 브랜치 → PR 생성
6. dalcli report로 결과 보고

## Rules
- main 직접 커밋 금지
- PR 생성 전 반드시 테스트 통과
- ccw session으로 작업 컨텍스트 유지
`

// defaultDalCueTemplate has one %s placeholder for the UUID.
const defaultDalCueTemplate = `uuid:    %q
name:    "dal"
version: "1.0.0"
player:  "claude"
model:   "haiku"
role:    "member"
skills:  ["skills/inbox-protocol", "skills/history-hygiene"]
hooks:   []
auto_task:      "1. /workspace/decisions/inbox/ → decisions.md 병합 (중복 제거 후 삭제). 2. /workspace/history-buffer/ → .dal/{name}/history.md 병합. 3. /workspace/wisdom-inbox/ → wisdom.md 병합. 4. history.md 12KB 초과 시 압축. 5. decisions.md 50KB 초과 시 30일+ 아카이브. 6. README.md, CLAUDE.md 갱신 필요 시 ccw tool update_module_claude로 자동 생성. 7. 변경 시 git add + commit + push."
auto_interval:  "30m"
git: {
	user:         "dal-docs"
	email:        "dal-docs@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
`

const defaultDalCharter = `# dal — 문서 관리자

## Role
팀 공유 기억의 유일한 writer + committer. 백그라운드 자동 실행.

## Responsibilities
1. decisions/inbox/ → decisions.md 병합 (중복 제거: By + What 기준)
2. wisdom-inbox/ → wisdom.md 병합
3. history-buffer/{name}.md → .dal/{name}/history.md 병합
4. history.md 12KB 초과 시 Core Context 압축
5. decisions.md 50KB 초과 시 30일+ 항목 → decisions-archive.md
6. README.md / CLAUDE.md 갱신 (ccw tool update_module_claude)
7. 변경 시 git add + commit + push

## Tools
- ccw tool update_module_claude — 모듈 문서 자동 생성
- ccw tool detect_changed_modules — 변경 모듈 탐지
- ccw memory — 컨텍스트 메모리 관리
- dalcli status / report

## Rules
- push 실패 시 재시도만. force push, reset 금지.
- 병합 후 inbox 파일 삭제.
- history에는 최종 결과만. 중간 상태 금지.
- 코드, 리뷰, 테스트 금지 — 문서만 담당.
`

const defaultSpec = `// dal.spec.cue — localdal schema

#Player: "claude" | "codex" | "gemini"
#Role:   "leader" | "member" | "ops"

#BranchConfig: {
	base?: string | *"main"
}

#SetupConfig: {
	packages?: [...string]
	commands?: [...string]
	timeout?:  string | *"5m"
}

#DalProfile: {
	uuid!:           string & != ""
	name!:           string & != ""
	version!:        string
	player!:           #Player
	fallback_player?:  #Player
	role!:             #Role
	channel_only?:   bool
	skills?:         [...string]
	hooks?:          [...string]
	model?:          string
	player_version?: string
	auto_task?:      string
	auto_interval?:  string
	workspace?:      string
	branch?:         #BranchConfig
	setup?:          #SetupConfig
	git?: {
		user?:         string
		email?:        string
		github_token?: string
	}
}
`
