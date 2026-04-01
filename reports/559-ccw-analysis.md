# CCW (Claude-Code-Workflow) 통합 분석 리포트

**Issue:** #559
**Date:** 2026-04-01
**Repository:** https://github.com/catlog22/Claude-Code-Workflow (v7.2.29)

---

## 1. CCW 구조 요약

### 1.1 아키텍처 레이어

| 레이어 | 위치 | 역할 |
|--------|------|------|
| 스킬/명령 정의 | `.claude/skills/`, `.claude/commands/` | Claude Code 하네스에 주입되는 프롬프트 기반 워크플로우 |
| 에이전트 정의 | `.claude/agents/` | 24개 전문 서브에이전트 (planning, execution, review 등) |
| CLI 백엔드 | `ccw/src/` | TypeScript 기반 CLI (세션, 메모리, 이슈, 큐 관리) |
| 프론트엔드 | `ccw/frontend/` | React 대시보드 (WebSocket 실시간 업데이트) |
| 워크플로우 템플릿 | `.ccw/workflows/` | 재사용 가능한 워크플로우 정의 |

### 1.2 핵심 컴포넌트

- **51개 스킬**: workflow-plan, team-coordinate, review-code, security-audit 등
- **24개 에이전트**: team-worker, code-developer, tdd-developer, context-search-agent 등
- **메시지 버스**: JSONL 기반 에이전트 간 통신 (`.workflow/.team/{session}/.msg/messages.jsonl`)
- **세션 관리**: 5가지 세션 타입 (WFS-, TLS-, TC-, ANL-, lite-plan)
- **큐 스케줄러**: DAG 기반 의존성 해석, 동시성 제어 (기본 2개)
- **MCP 서버**: Model Context Protocol 통합 (Claude Code 하네스 직접 연동)
- **멀티 CLI**: Gemini/Qwen/Codex 호출 오케스트레이션

### 1.3 팀 조율 모델 (Beat Model)

```
callback/resume → Coordinator (완료 처리, 파이프라인 체크, 워커 스폰)
              → Workers (Phase 1-5 실행)
              → callback (SendMessage + TaskUpdate)
              → 다음 비트
```

이벤트 드리븐 방식으로 폴링 없이 조율. 동적 역할 생성(role-spec)으로 하드코딩 없는 팀 구성.

---

## 2. dalcenter vs CCW 아키텍처 비교

### 2.1 계층 비교

| 관점 | dalcenter | CCW |
|------|-----------|-----|
| **실행 단위** | Docker 컨테이너 (1 dal = 1 container) | Claude Code 서브에이전트 (1 agent = 1 프로세스) |
| **오케스트레이션** | dalcenter daemon (Go HTTP 서버) | Coordinator 스킬 + 큐 스케줄러 (Node.js) |
| **통신** | Mattermost 채널 + matterbridge | JSONL 메시지 버스 (파일 기반) |
| **작업 할당** | `dalcli-leader assign` → MM 멘션 | TodoWrite + SendMessage (Claude Code 내장) |
| **상태 관리** | JSON 파일 (tasks.json, claims.json) | 세션 디렉토리 + meta.json + messages.jsonl |
| **이슈 연동** | GitHub polling → leader 전달 (#526) | `/issue/discover` → `/issue/plan` → `/issue/execute` |
| **스킬/역할** | `.dal/template/skills/` (CLAUDE.md 주입) | `.claude/skills/` (Claude Code 네이티브) |
| **멀티 플레이어** | claude, codex, gemini (Docker 이미지 분리) | claude 기본 + ccw cli로 gemini/qwen 호출 |

### 2.2 핵심 차이점

**dalcenter의 강점 (CCW에 없는 것):**
- LXC/Docker 기반 물리적 격리 (보안, 자원 제한)
- Mattermost 기반 사람-에이전트 통신 (관제 가능)
- 크리덴셜 자동 갱신 (credential_watcher + VeilKey)
- 멀티 프로젝트 동시 관리 (포트별 분리)
- leader-member 위계 + 에스컬레이션 체계
- CUE 스키마 기반 설정 검증

**CCW의 강점 (dalcenter에 없는 것):**
- 51개 사전 정의 워크플로우 (TDD, 리뷰, 보안 감사, UI 디자인 등)
- DAG 기반 큐 스케줄러 (의존성 해석 + 동시성 제어)
- 세션 라이프사이클 (pause/resume/sync/complete)
- Beat Model 이벤트 조율 (coordinator → worker 패턴)
- MCP 서버 직접 통합
- 시맨틱 메모리 시스템 (SQLite + 임베딩)
- 멀티 CLI 오케스트레이션 (교차 검증)

---

## 3. 통합 가능성 평가

### 3.1 결합 모델: "CCW-inside-dal"

가장 현실적인 통합 방식은 **CCW를 dal 컨테이너 내부에 설치**하는 것.

```
dalcenter (인프라 계층)
├── Docker container: dal-dev
│   ├── Claude Code 하네스
│   ├── .claude/skills/  ← CCW 스킬 주입
│   ├── .claude/agents/  ← CCW 에이전트 주입
│   └── ccw CLI          ← CCW 백엔드 설치
```

**이유:**
- CCW는 Claude Code 하네스의 `.claude/` 디렉토리에 프롬프트 파일을 배치하는 구조
- dalcenter는 이미 `skills/` 디렉토리를 컨테이너에 바인드 마운트
- CCW의 Node.js CLI는 컨테이너 내부에서 독립 실행 가능

**필요 작업:**
1. `.dal/template/` 구조에 CCW `.claude/` 디렉토리 포함
2. Dockerfile에 Node.js + ccw CLI 설치 추가
3. `dalcenter sync`에서 CCW 스킬 업데이트 반영

### 3.2 충돌 분석

| 항목 | 충돌 여부 | 설명 |
|------|-----------|------|
| CLI 네임스페이스 | **없음** | dalcli는 `dalcli` 바이너리, CCW는 `ccw` 바이너리. 분리됨 |
| 스킬 디렉토리 | **낮음** | dalcenter `skills/` vs CCW `.claude/skills/`. 경로 다름 |
| CLAUDE.md | **주의** | dalcenter가 `instructions.md`를 `CLAUDE.md`로 주입. CCW도 CLAUDE.md 사용. 병합 필요 |
| 세션 디렉토리 | **없음** | CCW는 `.workflow/`에 저장. dalcenter는 사용하지 않음 |
| Git 워크플로우 | **낮음** | dalcli의 autoGitWorkflow와 CCW의 git-workflow 스킬이 겹칠 수 있음. 하나만 활성화 |
| 메시지 채널 | **없음** | dalcenter는 Mattermost, CCW는 파일 기반 메시지 버스. 독립 |

### 3.3 CLAUDE.md 병합 전략

dalcenter의 `instructions.md` (→ CLAUDE.md)와 CCW의 `.claude/` 설정이 공존해야 함.

```
CLAUDE.md (dalcenter 주입)
├── dal 역할, 통신 규칙, 코딩 원칙 (기존)
└── CCW 스킬 활성화 참조 추가

.claude/settings.json (CCW 설정)
├── 스킬 목록
├── 에이전트 정의
└── MCP 서버 설정
```

`.claude/settings.json`은 Claude Code가 자동 로드하므로 CLAUDE.md와 별도로 관리 가능.

---

## 4. 대체 가능한 기존 이슈 분석

### 4.1 #529 — 이슈 기반 자동 워크플로우 (OPEN)

**현재 구현:** #526(이슈 감지) + #527(tell) + #528(leader 생명주기) 조합으로 수동 파이프라인 구축 중.

**CCW 대체 가능 범위:**
- `/issue/discover` → 이슈 자동 발견
- `/issue/plan` → 이슈별 실행 계획 생성
- `/issue/execute` → DAG 기반 병렬 실행
- `team-planex` → 계획-실행 파이프라인

**평가:** CCW가 이슈→계획→실행 파이프라인을 이미 구현. 단, dalcenter의 leader-member 위계와 Docker 격리는 CCW에 없으므로 **부분 대체** 가능. 이슈 감지(#526)와 컨테이너 소환(#528)은 dalcenter 고유 영역으로 유지 필요.

### 4.2 #545 — dal 작업 이벤트 자동 알림 (OPEN)

**현재 구현 계획:** dalcli에서 PR 생성/이슈 완료 시 `notify-dalroot` 호출.

**CCW 대체 가능 범위:**
- CCW 세션 완료 시 콜백 → dalcenter daemon HTTP 엔드포인트로 전달 가능
- `ship` 스킬의 PR 생성 파이프라인에 후처리 훅 추가 가능

**평가:** CCW는 내부 세션 이벤트만 관리하며 외부 알림 채널(Mattermost)과의 연동은 없음. **대체 불가** — dalcenter 고유 기능으로 유지하되, CCW 세션 완료를 트리거로 활용하는 연동은 가능.

### 4.3 #526 — leader 이슈 자동 감지 (CLOSED)

이미 머지됨. dalcenter daemon의 `issue_watcher`가 GitHub 이슈를 polling하여 leader에게 전달. CCW의 `/issue/discover`는 다른 목적(코드 분석 기반 이슈 발굴)이므로 대체 관계 아님.

### 4.4 추가 — CCW가 강화할 수 있는 영역

| dalcenter 미구현 | CCW 스킬 | 효과 |
|------------------|----------|------|
| 코드 리뷰 자동화 | `review-code`, `review-cycle`, `team-review` | reviewer dal의 리뷰 품질 향상 |
| TDD 워크플로우 | `workflow-tdd-plan`, `tdd-developer` | dev dal의 개발 프로세스 체계화 |
| 보안 감사 | `security-audit` | OWASP Top 10 + STRIDE 자동 분석 |
| 테스트 자동화 | `team-testing`, `workflow-test-fix` | test-dal의 테스트 커버리지 향상 |
| 기술 부채 관리 | `team-tech-debt` | 체계적 부채 식별/해소 |
| 세션 관리 | `workflow:session:*` | 장시간 작업의 상태 보존/재개 |

---

## 5. 도입 권고사항

### 5.1 단계별 도입 계획

**Phase 1: 스킬 주입 (낮은 위험)**
- CCW `.claude/skills/`를 dal 템플릿에 포함
- 단일 스킬부터 시작: `review-code`, `investigate`
- CLAUDE.md 충돌 없이 `.claude/settings.json`으로 관리
- 예상 작업량: Dockerfile 수정 + 템플릿 디렉토리 추가

**Phase 2: 에이전트 활성화 (중간 위험)**
- `.claude/agents/` 전체 주입
- team-worker, code-developer 등 서브에이전트 활용
- dal별 활성화 스킬 목록을 `dal.cue`에서 관리

**Phase 3: CCW CLI 연동 (높은 위험)**
- ccw CLI를 컨테이너에 설치
- 세션 관리, 메모리, 큐 스케줄러 활용
- Node.js 의존성 추가 → 이미지 크기 증가 고려
- dalcenter API와 ccw 세션 이벤트 연동

### 5.2 하지 말아야 할 것

- dalcenter의 Docker 격리 모델을 CCW의 프로세스 모델로 교체하지 말 것
- Mattermost 통신을 CCW 메시지 버스로 대체하지 말 것 (사람-에이전트 통신 필수)
- dalcli/dalcli-leader CLI를 ccw CLI로 대체하지 말 것 (인프라 관리 vs 워크플로우 관리는 다른 관심사)
- CLAUDE.md 를 CCW 형식으로 완전 교체하지 말 것 (dal 위계/통신 규칙 유지 필수)

### 5.3 리스크

| 리스크 | 영향 | 완화 |
|--------|------|------|
| 이미지 크기 증가 | Node.js + ccw 의존성 | 별도 레이어로 분리, 필요 스킬만 포함 |
| CLAUDE.md 충돌 | dal 규칙과 CCW 규칙 혼재 | `.claude/settings.json` 분리 관리 |
| 업스트림 의존성 | CCW 업데이트 추적 필요 | git submodule 또는 정기 sync |
| 복잡성 증가 | 디버깅 난이도 상승 | Phase 1에서 단일 스킬로 검증 후 확대 |

---

## 6. 결론

CCW는 dalcenter와 **보완 관계**에 있다. 경쟁이 아닌 계층 분리:

- **dalcenter** = 인프라 계층 (컨테이너 격리, 크리덴셜, 통신, 생명주기)
- **CCW** = 워크플로우 계층 (작업 계획, 실행 패턴, 리뷰, 테스트)

"CCW-inside-dal" 모델로 단계적 도입하면, dalcenter가 직접 구현해야 했던 워크플로우 로직(#529 부분 대체)을 CCW에 위임하고, dalcenter는 인프라 관리에 집중할 수 있다.

**즉시 도입 추천:** Phase 1 (스킬 주입)은 위험 낮고 효과 높음. `review-code`와 `investigate` 스킬을 reviewer/dev dal에 주입하여 검증 시작 권고.
