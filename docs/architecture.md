# dalcenter 아키텍처

## 전체 구조

```
LXC: dalcenter
├── dalcenter serve              # 데몬
│   ├── HTTP API (:11190)        # CLI 명령 수신
│   ├── soft-serve (:23231)      # localdal git 호스팅 + webhook
│   └── Docker 관리              # dal 컨테이너 생명주기
│
├── Docker: leader (claude)      # dalcli-leader 내장
├── Docker: dev (claude)         # dalcli 내장
├── Docker: dev-2 (claude)       # 복수 소환 가능
└── Docker: reviewer (codex)     # player별 다른 이미지
    └── 각 Docker ←소켓→ dalcenter

서비스 레포 (호스트 또는 다른 LXC)
└── your-project/
    └── .dal/ ←subtree→ soft-serve
```

## 바이너리 3개

| 바이너리 | 위치 | 역할 |
|---|---|---|
| dalcenter | LXC 호스트 | 운영자 — 인프라 + 전체 관리 |
| dalcli-leader | leader 컨테이너 | 팀장 — 팀원 관리 + 작업 지시 |
| dalcli | member 컨테이너 | 팀원 — 상태 조회 + 보고 |

## 스코프 매트릭스

```
                    dalcenter (운영자)    dalcli-leader (팀장)    dalcli (팀원)
인프라
  serve             ✅                    -                       -
  init              ✅                    -                       -
  validate          ✅                    -                       -
생명주기
  wake              ✅ 전체               ✅ 본인 팀              -
  sleep             ✅ 전체               ✅ 본인 팀              -
관찰
  ps                ✅ 전체               ✅ 본인 팀              ✅ 본인 팀
  status            ✅ 전체               ✅ 본인 팀              ✅ 본인만
  logs              ✅ 전체               ✅ 본인 팀              -
  attach            ✅ 전체               ✅ 본인 팀              -
동기화
  sync              ✅                    ✅                      -
협업 (Mattermost)
  assign            -                    ✅ 팀원에게 지시         -
  report            -                    -                       ✅ 팀장에게 보고
```

## 전제

- 1 localdal = 1 팀
- leader 1명 + member N명
- 인증 없음 (같은 LXC 내부 통신)

## localdal (.dal/)

서비스 레포당 1개. git subtree로 연결. SSOT.

```
.dal/
  dal.spec.cue              스키마 정의
  leader/
    dal.cue                 uuid, player, role:leader
    instructions.md         → wake 시 CLAUDE.md로 변환
  dev/
    dal.cue                 uuid, player, role:member
    instructions.md
  skills/                   공유 스킬 풀
    code-review/SKILL.md
```

## wake 흐름

```
dalcenter wake dev

  1. .dal/dev/dal.cue 읽기 → player, skills, git config
  2. Docker 컨테이너 생성 (dalcenter/claude:latest)
  3. instructions.md → CLAUDE.md 변환 (bind mount)
  4. skills/ → ~/.claude/skills/ (bind mount)
  5. .credentials.json 마운트 (read-only)
  6. 서비스 레포 → /workspace (bind mount)
  7. GitHub 토큰 주입 (dal.cue git.github_token)
  8. dalcli / dalcli-leader 바이너리 주입 (docker cp)
  9. Mattermost 봇 계정 생성 + 채널 참가
  10. 환경변수: DAL_NAME, DAL_UUID, DAL_ROLE, DALCENTER_URL, GH_TOKEN
```

## sync 흐름

```
.dal/skills/code-review/SKILL.md 수정 → git push
  → soft-serve post-receive hook
  → curl POST dalcenter:11190/api/sync
  → bind mount라 컨테이너에서 즉시 반영
```

## Mattermost 통신

- 프로젝트당 채널 1개 (serve 시 자동 생성)
- dal별 봇 계정 (wake 시 자동 생성)
- assign → @mention으로 작업 지시
- report → [dal-name] 보고

## Docker 이미지

```
dalcenter/claude:latest    ubuntu + nodejs + claude-code + gh CLI
dalcenter/codex:latest     ubuntu + nodejs + codex
dalcenter/gemini:latest    ubuntu + python3 + gemini-cli
```

## 환경변수

```
운영자:
  DALCENTER_URL               데몬 주소 (기본: http://localhost:11190)
  DALCENTER_LOCALDAL_PATH     localdal 경로 (기본: .dal/)
  DALCENTER_MM_URL            Mattermost URL
  DALCENTER_MM_TOKEN          Mattermost admin token
  DALCENTER_MM_TEAM           Mattermost team name

컨테이너 내 (wake 시 자동):
  DAL_NAME                    dal 이름
  DAL_UUID                    dal UUID
  DAL_ROLE                    leader / member
  DAL_PLAYER                  claude / codex / gemini
  DALCENTER_URL               데몬 주소
  GH_TOKEN / GITHUB_TOKEN     GitHub 인증
  GIT_AUTHOR_NAME/EMAIL       git 커밋 정보
```
