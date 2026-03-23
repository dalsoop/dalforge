# dalcenter 아키텍처 도안

## 전체 구조

```
┌─────────────────────────────────────────────────────┐
│                    soft-serve (LXC)                  │
│              경량 git 서버 (바이너리 하나)              │
│                                                     │
│  veilkey-localdal.git   myproject-localdal.git      │
│         │                       │                   │
│         └───── webhook ─────────┘                   │
│                   │                                 │
│            dalcenter sync                           │
└─────────────────────────────────────────────────────┘
                    │
        ┌───────────┴───────────┐
        ▼                       ▼
┌──────────────┐       ┌──────────────┐
│  veilkey     │       │  my-project  │
│  -selfhosted │       │              │
│              │       │              │
│  .dal/ ◄─subtree     │  .dal/ ◄─subtree
│    dev/      │       │    ops/      │
│    reviewer/ │       │              │
│    skills/   │       │    skills/   │
│              │       │              │
└──────┬───────┘       └──────┬───────┘
       │                      │
       ▼                      ▼
┌──────────────┐       ┌──────────────┐
│  LXC 300     │       │  LXC 400     │
│  dal: dev    │       │  dal: ops    │
│  player:     │       │  player:     │
│   claude     │       │   codex      │
│  CLAUDE.md ◄─┤       │  AGENTS.md ◄─┤
│  skills/ ◄───┤       │  skills/ ◄───┤
└──────────────┘       └──────────────┘
┌──────────────┐
│  LXC 301     │
│  dal: reviewer│
│  player:     │
│   claude     │
│  CLAUDE.md ◄─┤
│  skills/ ◄───┤
└──────────────┘
```

## localdal 레포 구조 (서비스 레포당 1개)

```
veilkey-localdal/
│
├── dal.spec.cue                # 스키마 정의 (모든 dal.cue가 따르는 규칙)
│
├── dev/                        # dal 1명 = 폴더 1개
│   ├── dal.cue                 # UUID, player, 스킬 참조
│   ├── instructions.md         # 지시사항 원본 (소환시 CLAUDE.md로 변환)
│   └── hooks/
│       └── pre-push.sh
│
├── reviewer/                   # dal 1명
│   ├── dal.cue
│   └── instructions.md
│
└── skills/                     # 공유 스킬 풀 (여러 dal이 참조)
    ├── code-review/
    │   └── SKILL.md
    ├── testing/
    │   └── SKILL.md
    └── infra-ops/
        └── SKILL.md
```

## dal.cue 예시

```cue
// dev/dal.cue
uuid:    "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
name:    "dev"
version: "1.0.0"
player:  "claude"

skills: [
    "skills/code-review",
    "skills/testing",
]

hooks: [
    "hooks/pre-push.sh",
]

container: {
    base:     "ubuntu:24.04"
    packages: ["bash", "git", "nodejs"]
}
```

## dalcenter 명령어 체계

```
dalcenter
├── dal                          # dal 관리
│   ├── create <name> --player   # dal 폴더 + dal.cue 생성
│   ├── delete <name>            # dal 삭제 (소환중이면 경고)
│   ├── list                     # 모든 dal 목록
│   ├── config <name>            # dal.cue 편집
│   ├── up <name> --repo --vmid  # 소환 (컨테이너 생성 + 배치)
│   └── down <name>              # 해산 (컨테이너 정리)
│
├── skill                        # 스킬 관리
│   ├── create <name>            # skills/<name>/ 폴더 생성
│   ├── delete <name>            # 스킬 삭제 (사용중이면 경고)
│   ├── add <dal> <skill>        # dal에 스킬 연결
│   ├── remove <dal> <skill>     # dal에서 스킬 해제
│   └── list [dal]               # 스킬 목록 (dal 지정시 그 dal의 스킬만)
│
├── sync                         # 일괄 배포
│   └── [--all]                  # 변경 감지 → 영향받는 dal 재배포
│
├── status [name]                # 상태 조회
└── init                         # localdal 레포 초기화
```

## 소환 (dal up) 상세 흐름

```
dalcenter dal up dev --repo /root/jeonghan/repository/veilkey-selfhosted --vmid 300

  1. .dal/dev/dal.cue 읽기
     → uuid: a1b2c3d4
     → player: claude
     → skills: [code-review, testing]
     → container.base: ubuntu:24.04

  2. LXC 컨테이너 생성 (vmid 300)
     → pct create 300 ubuntu:24.04 ...
     → pct start 300
     → apt-get install bash git nodejs

  3. player 설치
     → player가 claude → npm install -g @anthropic-ai/claude-code
     → player가 codex  → npm install -g @openai/codex

  4. 파일 주입
     → instructions.md → CLAUDE.md로 이름 변환 (player=claude이므로)
     → 컨테이너 내 /root/.claude/CLAUDE.md에 복사

  5. 스킬 주입
     → .dal/skills/code-review/ → 컨테이너 내 /root/.claude/skills/code-review/
     → .dal/skills/testing/     → 컨테이너 내 /root/.claude/skills/testing/

  6. 인증 정보 동기화
     → proxmox-host-setup ai mount --vmid 300 --agent claude

  7. 레포 마운트/클론
     → 컨테이너에서 veilkey-selfhosted 레포 접근 가능하게

  8. registry 업데이트
     → uuid=a1b2c3d4, vmid=300, repo=veilkey, status=up

  9. 완료
     → "dev (a1b2c3d4) summoned → LXC 300, player=claude, skills=2"
```

## 해산 (dal down) 상세 흐름

```
dalcenter dal down dev

  1. registry에서 dev 조회 → vmid=300

  2. 컨테이너 정리
     → pct stop 300
     → pct destroy 300 --purge

  3. registry 업데이트
     → status=down, dismissed_at=now

  4. 완료
     → "dev (a1b2c3d4) dismissed ← LXC 300"
```

## 일괄 배포 (sync) 상세 흐름

```
[localdal에서 skills/code-review/SKILL.md 수정 → git push]

soft-serve webhook → dalcenter sync

  1. git diff로 변경된 파일 감지
     → skills/code-review/SKILL.md 변경됨

  2. 역참조: "code-review 스킬을 쓰는 dal은?"
     → registry 조회 → dev (LXC 300), reviewer (LXC 301)

  3. 영향받는 dal 컨테이너에 재배포
     → LXC 300: skills/code-review/ 재복사
     → LXC 301: skills/code-review/ 재복사

  4. 완료
     → "synced: skills/code-review → dev (300), reviewer (301)"
```

## 데이터 모델 (registry.db)

```sql
-- dal 정의
CREATE TABLE dals (
    uuid TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    folder TEXT NOT NULL,           -- 폴더명 (별명)
    player TEXT NOT NULL,           -- claude, codex, gemini
    localdal_repo TEXT,             -- soft-serve repo URL
    service_repo TEXT,              -- /path/to/veilkey
    version TEXT,
    created_at TEXT
);

-- dal ↔ 스킬 매핑
CREATE TABLE dal_skills (
    dal_uuid TEXT REFERENCES dals(uuid),
    skill_path TEXT,                -- "skills/code-review"
    PRIMARY KEY (dal_uuid, skill_path)
);

-- 소환된 인스턴스
CREATE TABLE instances (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    dal_uuid TEXT REFERENCES dals(uuid),
    vmid TEXT,                      -- LXC container ID
    status TEXT DEFAULT 'down',     -- up, down, error
    assigned_repo TEXT,             -- /path/to/veilkey
    summoned_at TEXT,
    dismissed_at TEXT,
    last_synced_at TEXT
);
```

## 파일명 변환 규칙

```
localdal 원본          player       컨테이너에 주입되는 파일
─────────────────────────────────────────────────────────
instructions.md   →   claude   →   CLAUDE.md
instructions.md   →   codex    →   AGENTS.md
instructions.md   →   gemini   →   GEMINI.md
```

## 환경변수

```
DALCENTER_DATA              # dalcenter 데이터 경로 (기본: ~/.dalcenter)
DALCENTER_LOCALDAL_PATH     # localdal 경로 (기본: .dal/)
DALCENTER_SOFT_SERVE_URL    # soft-serve SSH URL
```
