<div align="center">
  <h1>dalcenter</h1>
  <p><strong>Dal 생명주기 관리자 — AI 에이전트 컨테이너를 깨우고, 재우고, 동기화</strong></p>
  <p>
    <a href="https://github.com/dalsoop/dalcenter"><img src="https://img.shields.io/badge/github-dalsoop%2Fdalcenter-181717?logo=github&logoColor=white" alt="GitHub repository"></a>
    <a href="./LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-2563eb.svg" alt="AGPL-3.0 License"></a>
  </p>
  <p><a href="./README.md">English</a></p>
</div>

dalcenter는 dal(AI 인형)을 관리합니다. Claude Code, Codex, Gemini가 설치된 Docker 컨테이너를 각각의 스킬, 지시사항, git 인증으로 구성합니다. 템플릿은 git(localdal)으로 관리하고, dalcenter는 런타임을 담당합니다.

## 설치

### 사전 요구사항

- **Go** 1.25.0+
- **Docker**
- **Git**
- **Mattermost** 서버 및 admin 토큰

### 빌드

```bash
go build -o /usr/local/bin/dalcenter ./cmd/dalcenter/
go build -o /usr/local/bin/dalcli ./cmd/dalcli/
go build -o /usr/local/bin/dalcli-leader ./cmd/dalcli-leader/
```

### Docker 이미지 빌드

dal을 소환하기 전에 최소한 base 이미지를 빌드해야 합니다:

```bash
cd dockerfiles && docker build -t dalcenter/claude:latest -f claude.Dockerfile .
```

기타 이미지: `claude-go.Dockerfile`, `claude-rust.Dockerfile`, `codex.Dockerfile`, `gemini.Dockerfile`.

### 환경변수

| 변수 | 설명 |
|------|------|
| `DALCENTER_URL` | dalcenter API 주소 (예: `http://localhost:11190`) |

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
  → HTTP API 시작
  → repo-watcher 시작 (2분 간격 git fetch/pull)
  → cred-watcher 시작 (토큰 만료 자동 갱신)

dalcenter wake dev
  → .dal/dev/dal.cue 읽기
  → Docker 컨테이너 생성 (dalcenter/claude:latest)
  → instructions.md → CLAUDE.md 변환 주입
  → 스킬, 인증, 서비스 레포 마운트
  → dalcli 바이너리 주입
  → dal이 작업 시작

git push (GitHub)
  → repo-watcher가 원격 변경 감지 (2분 이내)
  → git pull --ff-only
  → .dal/ 변경 시 → 실행 중인 컨테이너에 자동 sync
```

## 구조

```
LXC: dalcenter
├── dalcenter serve          HTTP API + Docker 관리
│   ├── repo-watcher         git fetch/pull → 자동 sync
│   └── cred-watcher         토큰 만료 → 자동 갱신
├── Docker: leader (claude)  dalcli-leader 내장
├── Docker: dev (claude)     dalcli 내장
└── Docker: dev-2 (claude)   복수 소환 가능
```

## CLI

```
dalcenter serve                   # 데몬 (HTTP API + repo-watcher + Docker)
dalcenter init --repo <path>      # localdal 초기화 (.dal/ + subtree)
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
channel_only: true
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

## 인증 (Credentials)

dalcenter는 player별 인증 정보를 컨테이너에 자동 마운트합니다 (read-only). wake 시 토큰 만료 경고.

| Player | 호스트 경로 | 컨테이너 경로 | 만료 체크 |
|--------|-----------|-------------|----------|
| claude | `~/.claude/.credentials.json` | `~/.claude/.credentials.json` | `expiresAt` (ms) |
| codex | `~/.codex/auth.json` | `~/.codex/auth.json` | `tokens.expires_at` (RFC3339) |
| gemini | env `GEMINI_API_KEY` | env `GEMINI_API_KEY` | — |

### Proxmox LXC 환경

```bash
pve-sync-creds [CT_ID]   # 기본값: 105
```

`tee`로 in-place 쓰기 → 파일 inode 보존 → Docker bind mount 유지.

### 토큰 갱신

- **Claude**: 만료 시 호스트에서 `claude auth login` → `pve-sync-creds`
- **Codex**: 만료 시 호스트에서 `codex auth login` → `pve-sync-creds`
- **Gemini**: API 키 (만료 없음). `GEMINI_API_KEY` 환경변수 설정.
- 실행 중인 dal이 인증 실패를 만나면 이제 dalcli가 host action용 claim을 자동 생성하고, 채널에는 짧은 credential sync 안내만 남깁니다.
- `DALCENTER_CRED_OPS_ENABLED`가 켜져 있으면(기본값), dalcenter가 이 credential sync 흐름을 자동 실행하려고 시도하고 `dal-ops` 채널에 진행 상태를 남깁니다.
- dalcenter가 LXC 안에 떠 있어서 `proxmox-host-setup`나 `pve-sync-creds`를 직접 실행할 수 없으면 `DALCENTER_CRED_OPS_HTTP_URL`, `DALCENTER_CRED_OPS_HTTP_TOKEN`으로 호스트 bridge를 붙여야 합니다.
- 예시 bridge 서버는 [`scripts/dalcenter-cred-ops-httpd.py`](./scripts/dalcenter-cred-ops-httpd.py)에 있고, `POST /sync`로 들어온 요청에 대해 Proxmox 호스트에서 `proxmox-host-setup ai sync --agent <player>` 후 `pve-sync-creds <vmid>`를 실행합니다.

### Channel-only 팀

dal profile에 `channel_only: true`를 넣으면 Mattermost DM polling을 끄고, 프로젝트 채널과 해당 스레드에서만 반응합니다.

## 기여

[`CONTRIBUTING.md`](./CONTRIBUTING.md) 참고.
