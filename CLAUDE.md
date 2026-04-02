# dalcenter

dal(AI 인형) 생명주기 관리 런타임. 코드 구조와 CLI는 `README.md`, `CONTRIBUTING.md`, `docs/architecture.md` 참조.

---

## GitHub 조직

| 조직 | 용도 |
|------|------|
| **dalsoop** | dal 인프라 — dalcenter, dalroot, localdal 관련 레포 |
| **veilkey** | 시크릿 관리 — VeilKey Center, LocalVault, veil CLI |

레포 목록: `gh repo list <org>` 으로 확인.

---

## 레포 작업 원칙

1. **README.md 먼저 읽기** — 모든 레포 진입 시 README부터 확인
2. **main 직접 커밋 금지** — 반드시 브랜치 + PR
3. **.dal/ 코드는 dalroot가 관리** — 직접 수정 금지, 이슈로 요청
4. **decisions.md / wisdom.md 직접 수정 금지** — scribe dal이 inbox에서 병합

---

## dalcenter 운영

### 팀 확인

```bash
# 현재 실행 중인 팀 목록
systemctl list-units 'dalcenter@*'

# 팀별 환경 설정
ls /etc/dalcenter/*.env

# 팀별 포트 확인
grep -h 'PORT\|addr' /etc/dalcenter/*.env
```

### 팀 상태

```bash
# 전체 dal 상태
dalcenter ps

# 개별 dal 상태
dalcenter status <dal-name>
```

### 바이너리 교체

dalcenter 바이너리를 교체하면 **모든 팀 인스턴스를 재시작**해야 한다.

```bash
# 빌드 후 전체 재시작
go build -o /usr/local/bin/dalcenter ./cmd/dalcenter/
systemctl restart 'dalcenter@*'
```

### 자동화 dal

| dal | 역할 |
|-----|------|
| doctor | 헬스체크 — 팀/dal 상태 모니터링 |
| custodian | 자동 커밋 — 정기 정리 작업 |
| scribe | 메모리 품질 — CLAUDE.md + memory 파일 감사 |
| scheduled dalroot | 이슈 감시 + 리마인드 — dal-control 채널에 보고 |
| ops-watcher | 팀 헬스 자동 복구 — leader 자동 wake |
| queue-manager | leader 큐 관리 — 만료·동시 실행 제한 |
| auditor | dalroot 원칙 감사 |
| config-manager | charter/설정 전체 팀 동기화 |
| test-writer | PR별 테스트 자동 작성 |
| token-optimizer | 토큰 사용량 분석 |
| standup-manager | dal-daily 채널 업무 수집 |

---

## dal 작업 흐름

### 수동 흐름 (기존)

```
이슈 생성 (GitHub)
  → MM 채널에서 @dal-leader 멘션
  → leader가 적절한 dal에 assign
  → dal이 브랜치 + PR 생성
  → dalroot가 리뷰 후 머지
```

### 자동화 파이프라인

```
사람 (이슈 생성)
  → scheduled dalroot (감시 + 리마인드)
  → dal 팀 (구현)
  → dal-control 채널 (보고)
  → 사람 (이모지/코멘트로 승인·방향 제시)
```

scheduled dalroot가 GitHub 이슈를 감시하고, 적절한 dal 팀에 작업을 전달한다.
결과는 dal-control 채널에 보고되며, 사람이 비동기로 승인/방향을 결정한다.

### MM 채널 구조

| 채널 | 용도 |
|------|------|
| dal-control | 인프라 관제 — scheduled dalroot 보고, 사람 승인 |
| dal-daily | 스탠드업 — standup-manager 업무 수집 |
| dalroot-log | dalroot 활동 로그 |
| dalroot-pipeline | 파이프라인 상태 보고 |

채널 = 방(Room) — dalbridge가 1:1 매핑 + ACK.

### 아키텍처 원칙

- **ACK 기반 통신** — fire-and-forget 금지, 모든 메시지에 수신 확인
- **done criteria 체크리스트** — 이슈 완료 시 전체 팀 공통 강제
- **PR 6단계 게이트** — CI → 리뷰 → 테스트 → 통합 → 머지 → 배포

---

## 메모리 작성 원칙

- CLAUDE.md와 memory 파일은 **scribe dal이 전담**
- dalroot가 직접 쓰지 않는다 → 이슈로 요청
- **하드코딩 금지** — IP, 포트, VMID, 토큰 절대 넣지 말 것
- **조회 방법만 기술** — 값이 아니라 명령어를 적을 것
- **코드/git에서 알 수 있는 건 적지 말 것**
- memory 파일과 CLAUDE.md 간 **중복 금지**

---

## 인프라 조회 방법

| 대상 | 명령어 |
|------|--------|
| LXC 목록 | `pct list` |
| VM 목록 | `qm list` |
| dalcenter 팀 | `systemctl list-units 'dalcenter@*'` |
| 팀별 포트 | `/etc/dalcenter/*.env` 확인 |
| Traefik 설정 | dalcenter LXC 내 `dynamic.yml` (단일 파일) |
| Cloudflare 키 경로 | `/root/.acme.sh/account.conf` |
| 스트림 키 | VeilKey LocalVault — `veil get <key-name>` |
| dal 환경변수 | `dalcenter attach <dal>` 후 `env` |

---

## 삽질 교훈

이 섹션의 항목은 실제 장애/삽질에서 얻은 것이다. 관련 작업 시 반드시 참고.

### 데스크톱 환경
- Cinnamon은 소프트웨어 렌더링으로 CPU 95% 유발 → **XFCE 사용**

### ffmpeg 스트리밍
- CBR 설정: `nal-hrd=cbr` 필수 (minrate만으로 안 됨)
- 오디오: `sine` 소스 사용 (`anullsrc`는 YouTube에서 bitrate 0 처리)

### Traefik
- `dynamic.yml` **단일 파일만 읽음** — `conf.d/` 디렉토리 방식 안 됨

### dalcenter 운영
- 바이너리 교체 후 **전체 팀 재시작 확인** — 일부만 재시작하면 버전 불일치
- MM 봇 토큰은 **팀별 분리 필수** — 공유 시 self-message 무시 문제 발생

### dalbridge / 통신
- fire-and-forget은 메시지 유실 원인 — **반드시 ACK 기반**으로 설계
- listener IGNORE_USERS는 해당 사용자 메시지 **전부 드롭** — 필터 범위 주의
- dal 컨테이너에서 PVE 호스트 직접 접근 불가 — **중간 LXC 경유** 필요 (`pct list`로 확인)
