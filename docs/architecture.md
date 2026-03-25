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
  VEILKEY_LOCALVAULT_URL      VeilKey localvault (있으면 전달)
```

## 시크릿 흐름

```
VeilKey LocalVault (LXC 내)
  → dalcenter (veil resolve / localvault API)
  → 환경변수로 Docker 컨테이너에 주입
  → 컨테이너는 평문으로 사용 (GH_TOKEN, API keys 등)
```

dal.cue에서 시크릿 참조 방식:
- `"env:GITHUB_TOKEN"` — dalcenter 프로세스의 환경변수에서 가져옴
- `"VK:LOCAL:xxxx"` — veil resolve로 평문 변환 후 주입
- 직접 값 — 그대로 주입 (비추천)

## 배포 가이드

### 1. LXC 생성

```bash
pct create <VMID> local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst \
  --hostname dalcenter \
  --storage local-lvm --rootfs local-lvm:16 \
  --memory 4096 --cores 4 \
  --net0 name=eth0,bridge=vmbr1,ip=10.50.0.105/16,gw=10.50.0.1,type=veth \
  --unprivileged 0
```

LXC config에 추가 (Docker 지원):
```
lxc.apparmor.profile: unconfined
lxc.cap.drop:
lxc.mount.auto: proc:rw sys:rw
```

### 2. 패키지 설치

```bash
apt-get install -y docker.io git curl ca-certificates
# Go 1.25
curl -sL https://go.dev/dl/go1.25.0.linux-amd64.tar.gz | tar -C /usr/local -xzf -
```

### 3. VeilKey LocalVault 설치

```bash
cd /root/veilkey-selfhosted
VEILKEY_CENTER_URL=https://<VC_HOST>:11181 VEILKEY_NAME=dalcenter-lv \
  bash install/common/install-localvault.sh
# init + unlock
echo '<MASTER_PASSWORD>' | veilkey-localvault init --root --center https://<VC_HOST>:11181
curl -X POST http://localhost:10180/api/unlock -d '{"password":"<MASTER_PASSWORD>"}'
```

veil CLI:
```bash
VEILKEY_URL=http://localhost:10180 bash install/common/install-veil-cli.sh
```

### 4. systemd 서비스

```ini
# /etc/systemd/system/localvault.service
[Unit]
Description=VeilKey LocalVault
After=network.target
[Service]
Type=simple
EnvironmentFile=/root/.localvault/.env
ExecStart=/root/.localvault/veilkey-localvault
Restart=always
[Install]
WantedBy=multi-user.target

# /etc/systemd/system/dalcenter.service
[Unit]
Description=DalCenter Daemon
After=network.target docker.service localvault.service
Requires=docker.service
[Service]
Type=simple
Environment=PATH=/usr/local/bin:/usr/local/go/bin:/root/go/bin:/usr/bin:/bin
Environment=HOME=/root
Environment=DALCENTER_LOCALDAL_PATH=/root/<project>/.dal
Environment=GITHUB_TOKEN=<token>
ExecStart=/usr/local/bin/dalcenter serve --addr :11190 --repo /root/<project> --mm-url http://<MM_HOST>:8065 --mm-token <BOT_TOKEN> --mm-team <TEAM>
Restart=always
[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable localvault dalcenter
systemctl start localvault dalcenter
```

### 5. Docker 이미지 빌드

```bash
docker build -t dalcenter/claude:latest -f dockerfiles/claude.Dockerfile dockerfiles/
```

### 네트워크 요구사항

- dalcenter LXC: 내부 대역 (vmbr1, 10.50.0.x) — VaultCenter/Mattermost 접근 필요
- Docker: nesting=1 + apparmor unconfined
- Mattermost: 봇 토큰 (만료 없음) 필요

## 멀티 프로젝트 운영

하나의 dalcenter LXC에서 여러 프로젝트를 동시에 운영할 수 있다.
각 프로젝트는 별도 포트의 dalcenter 인스턴스로 관리한다.

### 구조

```
LXC: dalcenter
├── dalcenter.service         (:11190) — 프로젝트 A (예: veilkey)
├── dalcenter-gaya.service    (:11191) — 프로젝트 B (예: bridge-of-gaya)
├── dalcenter-xxx.service     (:11192) — 프로젝트 C ...
│
├── Docker: dal-dev           (프로젝트 A)
├── Docker: dal-leader        (프로젝트 B)
├── Docker: dal-architect     (프로젝트 B)
├── Docker: dal-writer        (프로젝트 B)
└── ...
```

Mattermost 채널은 프로젝트별로 자동 생성된다.

### 두 번째 인스턴스 추가

1. 서비스 레포 클론:
```bash
cd /root
git clone https://github.com/<org>/<project>.git
```

2. systemd 서비스 생성:
```ini
# /etc/systemd/system/dalcenter-<project>.service
[Unit]
Description=DalCenter Daemon — <Project>
After=network.target docker.service
Requires=docker.service
[Service]
Type=simple
Environment=PATH=/usr/local/bin:/usr/local/go/bin:/root/go/bin:/usr/bin:/bin
Environment=HOME=/root
Environment=DALCENTER_LOCALDAL_PATH=/root/<project>/.dal
Environment=GITHUB_TOKEN=<token>
ExecStart=/usr/local/bin/dalcenter serve --addr :<PORT> --repo /root/<project> --mm-url http://<MM_HOST>:8065 --mm-token <BOT_TOKEN> --mm-team <TEAM>
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
```

3. 시작:
```bash
systemctl daemon-reload
systemctl enable dalcenter-<project>
systemctl start dalcenter-<project>
```

4. dal 조작 시 환경변수 지정:
```bash
export DALCENTER_URL=http://localhost:<PORT>
export DALCENTER_LOCALDAL_PATH=/root/<project>/.dal
dalcenter ps
dalcenter wake leader
dalcenter wake --all
```

### 포트 규칙 (권장)

| 포트 | 프로젝트 |
|------|----------|
| 11190 | 기본 (첫 번째 프로젝트) |
| 11191 | 두 번째 프로젝트 |
| 11192 | 세 번째 프로젝트 |
| ... | 순차 증가 |

### 주의사항

- Docker 컨테이너 이름은 `dal-<dalname>` 형식. 프로젝트가 다르면 dal 이름도 달라야 컨테이너 충돌 안 남.
- 같은 LXC 내에서 리소스를 공유하므로, dal 수가 많으면 메모리/CPU 모니터링 필요.
- `dalcenter ps`는 `DALCENTER_URL`에 해당하는 인스턴스의 dal만 보여줌.
