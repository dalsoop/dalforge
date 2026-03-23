<div align="center">
  <h1>dalcenter</h1>
  <p><strong>Dal 생명주기 관리자 — AI 에이전트 컨테이너를 깨우고, 재우고, 동기화</strong></p>
  <p>
    <a href="https://github.com/dalsoop/dalcenter"><img src="https://img.shields.io/badge/github-dalsoop%2Fdalcenter-181717?logo=github&logoColor=white" alt="GitHub repository"></a>
    <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-2563eb.svg" alt="MIT License"></a>
  </p>
  <p><a href="./README.md">English</a></p>
</div>

dalcenter는 dal(AI 인형)을 관리합니다. Claude Code, Codex, Gemini가 설치된 Docker 컨테이너를 각각의 스킬, 지시사항, git 인증으로 구성합니다. 템플릿은 git(localdal)으로 관리하고, dalcenter는 런타임을 담당합니다.

## 빠른 시작

```bash
# 1. 데몬 시작
dalcenter serve --addr :11190 --repo /path/to/your-project \
  --mm-url http://mattermost:8065 --mm-token TOKEN --mm-team myteam

# 2. localdal 초기화
dalcenter init --repo /path/to/your-project

# 3. dal 템플릿 작성 (git으로)
# .dal/leader/dal.cue + instructions.md
# .dal/dev/dal.cue + instructions.md
# .dal/skills/code-review/SKILL.md

# 4. 검증
dalcenter validate

# 5. dal 소환
dalcenter wake leader
dalcenter wake dev
dalcenter ps

# 6. 작업 끝
dalcenter sleep --all
```

## 동작 방식

```
.dal/ (git 관리, localdal)
  leader/dal.cue + instructions.md     ← dal 템플릿
  dev/dal.cue + instructions.md
  skills/code-review/SKILL.md          ← 공유 스킬

dalcenter serve
  → soft-serve (git 서버) 시작
  → HTTP API 시작

dalcenter wake dev
  → .dal/dev/dal.cue 읽기
  → Docker 컨테이너 생성 (dalcenter/claude:latest)
  → instructions.md → CLAUDE.md 변환 주입
  → 스킬, 인증, 서비스 레포 마운트
  → dalcli 바이너리 주입
  → dal이 작업 시작
```

## 구조

```
LXC: dalcenter
├── dalcenter serve          HTTP API + soft-serve + Docker 관리
├── soft-serve               localdal git 호스팅 + webhook
├── Docker: leader (claude)  dalcli-leader 내장
├── Docker: dev (claude)     dalcli 내장
└── Docker: dev-2 (claude)   복수 소환 가능
```

## CLI

```
dalcenter serve                   # 데몬 (HTTP API + soft-serve + Docker)
dalcenter init --repo <path>      # localdal 초기화 (.dal/ + soft-serve + subtree)
dalcenter wake <dal> [--all]      # Docker 컨테이너 생성
dalcenter sleep <dal> [--all]     # Docker 컨테이너 정지
dalcenter sync                    # 변경사항 → 실행중인 dal에 반영
dalcenter validate [path]         # CUE 스키마 + 참조 검증
dalcenter status [dal]            # dal 상태
dalcenter ps                      # 소환된 dal 목록
dalcenter logs <dal>              # 컨테이너 로그
dalcenter attach <dal>            # 컨테이너 접속
```

### 컨테이너 안에서

```
dalcli-leader (팀장 전용)          dalcli (팀원)
  wake <dal>                        status
  sleep <dal>                       ps
  ps                                report <message>
  status <dal>
  logs <dal>
  sync
  assign <dal> <task>
```

## dal.cue

```cue
uuid:    "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
name:    "dev"
version: "1.0.0"
player:  "claude"
role:    "member"
skills:  ["skills/code-review", "skills/testing"]
hooks:   []
git: {
    user:         "dal-dev"
    email:        "dal-dev@myproject.dev"
    github_token: "env:GITHUB_TOKEN"
}
```

## localdal 구조

```
.dal/
  dal.spec.cue              스키마 정의
  leader/
    dal.cue                 uuid, player, role:leader
    instructions.md         → wake 시 CLAUDE.md로 변환
  dev/
    dal.cue                 uuid, player, role:member
    instructions.md
  skills/
    code-review/SKILL.md    여러 dal이 공유
    testing/SKILL.md
```

## 통신

dal 간 통신은 Mattermost. 프로젝트당 채널 1개 (serve 시 자동 생성).

- `dalcli-leader assign dev "작업"` → `@dal-dev 작업 지시: 작업` 전송
- `dalcli report "완료"` → `[dev] 보고: 완료` 전송

## 파일명 변환

| 원본 | player | 컨테이너 내 |
|---|---|---|
| instructions.md | claude | CLAUDE.md |
| instructions.md | codex | AGENTS.md |
| instructions.md | gemini | GEMINI.md |

## 기여

[`CONTRIBUTING.md`](./CONTRIBUTING.md) 참고.
