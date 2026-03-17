# dalforge-hub

`dalforge`는 `.dalfactory` 선언을 실제 로컬 런타임과 LXC 운영으로 바꾸는 self-hosted orchestration stack이다. `dalforge`는 패키지와 스펙을 유통하고, `dalcenter`는 사용자 레포의 `.dalfactory`를 읽어 등록하고 관리한다.

Korean summary: [`README.ko.md`](./README.ko.md)

## What It Is

가장 짧게 말하면:

- `dalforge`
  - 패키지와 스펙을 유통하는 클라우드 허브
- `dalcenter`
  - `.dalfactory`를 읽고 등록, export, 실행, provision을 담당하는 관리 주체
- 사용자 레포
  - `.dalfactory/`를 가진 실제 프로젝트

즉:

`.dalfactory`가 SSOT이고, `dalforge`는 배포하고, `dalcenter`는 실행하고 관리한다.

## Why It Exists

`dalforge`는 “에이전트 도구를 설치하는 것”에서 끝나지 않고, 레포 선언을 실제 실행 환경으로 연결하기 위해 존재한다.

문제는 보통 이렇다.

- 레포마다 스킬, 훅, 런타임 설정이 흩어짐
- 로컬 실행과 컨테이너 실행이 따로 놀기 쉬움
- 어떤 레포가 어떤 에이전트 환경을 요구하는지 재현이 어려움

`dalforge`는 이걸 `.dalfactory` 하나로 묶어서, 로컬 export와 Proxmox LXC까지 이어준다.

## 지금 되는 것

`dalcenter`는 현재 `.dalfactory`를 실제 운영 흐름으로 연결한다.

- `catalog search` 기반 dalforge 클라우드 패키지 조회
- `.dalfactory` validate
- `join/list/status` 기반 레포 등록 및 조회
- Claude/Codex skill export, Claude hook export/settings 반영
- `start/stop/restart` 로컬 프로세스 관리
- `reconcile/watch` 기반 drift 점검
- Proxmox LXC `provision/destroy`
- `container.packages` 설치
- `container.agents` 설치 명령 실행
- `dal summon/dismiss`와 export/unexport/destroy soft 연동

실제 Proxmox 라이브 검증도 끝났다.

- Ubuntu 24.04 LXC 생성
- `bash`, `python3`, `tmux` 설치 확인
- `destroy` 후 컨테이너 제거 확인

즉 지금은 “설계 문서만 있는 상태”가 아니라, 파생 레포의 `.dalfactory`에서 로컬 실행과 LXC 운영까지 이어지는 초기 운영 버전이다.

## Quick Start

가장 빠른 시작은 `dalcenter`로 패키지를 찾고, 레포를 등록하고, 상태를 보는 것이다.

```bash
dalcenter catalog search agent-coach
dalcenter join agent-coach
dalcenter list
dalcenter status agent-coach
```

## What Success Looks Like

실제 성공 예시는 이런 형태다.

```text
NAME                BRANCH  DESCRIPTION
dalcli-agent-coach  main    Tmux pane monitoring, stagnant detection, and LLM coachin...
```

```text
staged package: dalcli-agent-coach -> ~/.dalcenter/sources/dalcli-agent-coach
instance created: dal-... (template=default, source=cloud:dalcli-agent-coach, skills=2)
health check: ok
```

```text
DAL_ID                SOURCE       TEMPLATE  STATUS  HEALTH      SKILLS  CREATED
dal-...               agent-coach  default   ready   ok(0s ago)  2       ...
```

```text
source_type:    cloud
source_ref:     dalcli-agent-coach
health_status:  ok
```

## Docs Index

- [`docs/README.md`](./docs/README.md)
  - 전체 문서 입구
- [`docs/runbooks/first-join-and-provision.md`](./docs/runbooks/first-join-and-provision.md)
  - 처음 패키지를 등록하고 LXC까지 띄우는 가장 짧은 운영 runbook

## 한 줄 구조

- `dalforge`: 클라우드 허브. 패키지와 스펙을 유통한다.
- `dalcenter`: 등록/관리 주체. `.dalfactory`를 읽고 상태를 관리한다.
- 사용자 레포: 실제 프로젝트 레포. 여기에 `.dalfactory/`가 있다.

한 줄로 줄이면:

`dalforge`는 배포한다. `dalcenter`는 관리한다. `.dalfactory`는 사용자 레포에 있다.

## 구조

```
dalforge-hub/
  dalcenter/                     중앙 레지스트리 + 시크릿 관리
    dal.spec.cue                 핵심 스펙 (CUE)
  dalcli/                        CLI 도구 패키지들
    dalcli-agent-coach           에이전트 pane 감시 + 코칭
    dalcli-custom-functions      함수 레지스트리 + 명령어 이력
    dalcli-task-queue            작업 큐 + 순차 실행
    dalcli-lxc-stage-player      LXC stage 실행 진입점
    dalcli-agent-tool-syncer     문서 SSOT 동기화 + 링크 감시
    dalcli-agent-bridge          에이전트 간 릴레이
```

## 핵심 개념

### dal (인형)

dal은 AI 에이전트 인스턴스다. 컨테이너 안에 claude, codex, gemini 등이 이미 설치되고 로그인된 상태로 존재한다. 하나의 dal은 하나의 작업 환경이다.

### dalforge (클라우드 허브)

`dalforge`는 npm registry 같은 상위 유통/배포 허브다.

- 패키지 배포
- 스펙/문서 유통
- 버전 카탈로그

### dalcenter (등록 주체)

`dalcenter`는 `.dalfactory`를 읽고 등록하고 상태를 관리하는 주체다.

- 패키지(CLI/스킬/훅) 등록 및 버전 관리
- 인스턴스 생성 및 상태 추적
- 시크릿(API 키 등) 암호화 저장 및 배포
- 노드별 설치 현황(인벤토리) 관리
- 감사 이벤트 기록

### .dalfactory (레포 선언)

사용자 레포 루트에 위치하는 폴더다. `dalforge` 클라우드 허브 레포가 아니라, 실제 프로젝트 레포 안에 들어간다. 이 폴더가 실행, export, container, agents를 선언하는 SSOT다.

```
my-project/
  .dalfactory/
    dal.cue                      이 레포의 dal 정의
    templates/
      claude-dev.cue             claude 개발용 인형 틀
      claude-review.cue          claude 리뷰용 인형 틀
      codex-worker.cue           codex 작업용 인형 틀
      full-stack.cue             전체 에이전트 인형 틀
  src/
  ...
```

### PLAYER (에이전트)

실행 환경 안에서 실제로 일하는 주체다. 하나의 dal에 여러 PLAYER가 있을 수 있다. 각 PLAYER는 서로 다른 에이전트(claude, codex, gemini)일 수 있고, 서로 다른 도구 세트를 가진다.

## ID 체계

모든 dal 구성요소는 고유 ID를 가진다. 이름이 바뀌어도 ID는 영구 고정이다.

```
DAL:{CATEGORY}:{uuid8}

DAL:CLI:3a8c1f02          dalcli-agent-coach
DAL:CLI:7e4b9d15          dalcli-custom-functions
DAL:PLAYER:f1d24e83       claude-dev player
DAL:CONTAINER:a1b2c3d4    my-project container
```

### 카테고리

| 카테고리 | 설명 |
|---|---|
| CLI | 명령줄 도구 |
| PLAYER | 실행 환경 (에이전트) |
| CONTAINER | 컨테이너 서비스 |
| SKILL | 에이전트 스킬 |
| HOOK | 이벤트 훅 |

카테고리는 확장 가능하다. 새 카테고리 추가 시 dal.spec.cue 변경 없이 dalcenter에서 등록한다.

## 흐름

### 1. 템플릿 정의

.dalfactory/templates/claude-dev.cue 에 인형 틀을 정의한다. 컨테이너 base image, 설치할 패키지, 에이전트, CLI 도구, 스킬, 필요한 시크릿을 선언한다.

### 2. 레포 등록

```bash
dalcenter join /path/to/repo
```

현재 `join`은 아래를 수행한다.

1. 사용자 레포의 `.dalfactory/dal.cue` 읽기
2. manifest validate
3. skill/hook export
4. 로컬 instance dir 생성
5. registry + state 기록

```bash
dalcenter list
dalcenter status <name-or-id>
```

### 3. 빌드 및 Export

.dalfactory/ 가 소스(SSOT)이고, 각 에이전트용 설정은 빌드 산출물로 export된다.

1차 규칙은 레포 루트의 `skills/{name}/SKILL.md` 같은 원본 자산을 직접 export하는 것이다. 예외적으로 원본 허브 성격의 레포는 `source/document/skills/{name}/SKILL.md` 경로를 fallback export source로 선언할 수 있다.

```
.dalfactory/ (소스)
    -> export
.claude/
    skills/
    hooks/
    settings.json
.codex/
    skills/
```

현재 hook settings 반영은 Claude만 지원한다. Codex는 skills export까지만 지원한다.

### 4. 시크릿 관리

API 키 등 민감 정보는 자체 SecretVault에 암호화(AES-256-GCM) 저장된다. 컨테이너 안에서 에이전트가 실행될 때 자동으로 복호화되어 주입된다.

```bash
dalcenter secret set anthropic_api_key
dalcenter secret set openai_api_key
dalcenter secret list
```

### 5. 동기화

dalcenter는 등록된 레포와 런타임 상태를 주기적으로 동기화한다.

- 패키지 버전 업데이트 감지
- 설치 현황 보고
- 오프라인 시 캐시 모드로 동작

지금 실제 명령은 아래가 중심이다.

```bash
dalcenter reconcile
dalcenter watch --interval 60
```

## 실제 사용 예시

### 1. manifest 검증

```bash
dalcenter validate /root/dalforge-hub/dalcli/dalcli-agent-coach
```

### 2. dalforge 패키지 조회

```bash
dalcenter catalog search agent-coach
```

### 3. 레포 등록

```bash
dalcenter join /root/dalforge-hub/dalcli/dalcli-agent-coach
dalcenter list
dalcenter status dalcli-agent-coach
```

패키지 이름만으로도 dalforge에서 받아와서 등록할 수 있다.

```bash
dalcenter join agent-coach
```

### 4. 로컬 실행 관리

`build.entry`가 있으면 `--command` 없이도 실행된다.

```bash
dalcenter start dalcli-agent-coach
dalcenter status dalcli-agent-coach
dalcenter stop dalcli-agent-coach
```

성공 기준은 최소 이 정도다.

- `list`에서 `STATUS=ready`
- `HEALTH=ok(...)`
- `status`에서 `source_type`, `source_ref`, `health_status`가 보임

### 5. Proxmox LXC 생성

```bash
dalcenter provision dalcli-agent-coach \
  --vmid 219318 \
  --storage local-lvm \
  --bridge vmbr0 \
  --memory 512 \
  --cores 1
```

지원 플래그:

- `--vmid`
- `--storage`
- `--bridge`
- `--memory`
- `--cores`
- `--dry-run`

### 6. Proxmox LXC 제거

```bash
dalcenter destroy dalcli-agent-coach
```

### 7. dal과 연동

```bash
dal summon agent-coach
dal dismiss agent-coach
```

`dal summon`은 soft dependency로 export를 호출하고, `dal dismiss`는 unexport와 destroy를 soft dependency로 호출한다.

## 현재 한계

지금도 실사용은 가능하지만, 아래는 아직 확장 여지가 있다.

- 고급 네트워크/스토리지 정책
- disk size 같은 추가 운영 플래그
- hook 운영 예시 manifest
- Proxmox 대규모 운영용 정책/감사 정교화

## What It Is Not

- 단순 패키지 설치기만 있는 도구
- 클라우드 SaaS control plane만 있는 제품
- `.dalfactory` 없이 수동 설정만 전제하는 런타임
- 대규모 멀티테넌트 운영 플랫폼 완성형

## Tradeoffs

- 단순 스크립트보다 구조가 크고 개념이 많다
- Proxmox/LXC까지 다루면 운영 난이도가 올라간다
- `.dalfactory` 계약이 명확한 대신, 선언을 제대로 유지해야 한다

## Rough Comparison

| 도구 형태 | 기본 모델 | dalforge 차이점 |
|---|---|---|
| 단순 CLI 설치기 | 패키지 설치 후 끝 | dalforge는 `.dalfactory`를 읽고 등록, export, 상태, provision까지 연결 |
| 일반 task runner | 로컬 명령 실행 중심 | dalforge는 레포 선언과 런타임 상태를 함께 관리 |
| 컨테이너 provision 도구 | 인프라 생성 중심 | dalforge는 skill/hook export와 로컬 실행 흐름까지 같이 다룸 |

## Current Gaps

OpenClaw 같은 더 제품화된 agent stack과 비교하면, dalforge는 아직 아래가 약하다.

- agent-facing 진입점을 하나로 묶는 공용 gateway가 없다
- 세션 단위 context compaction 정책이 없다
- `.dalfactory` 기반 skill/hook discovery는 되지만 자동 등록 경험이 아직 약하다
- 서비스별 health 노출과 healthcheck 계약이 완전히 통일되진 않았다

즉 지금은 실제로 동작하는 운영 스택이지만, 첫인상은 아직 "잘 만든 self-hosted toolkit"에 더 가깝다.

## Near-Term Priorities

다음 단계는 이 순서가 맞다.

1. 공용 agent gateway 레이어 추가
2. `join/export` 시 skill/hook auto-discovery 강화
3. `/healthz`와 container healthcheck 규약 통일
4. 수동 split/reset 대신 session compaction policy 추가

## 스펙

모든 규약은 dalcenter/dal.spec.cue 에 CUE로 정의되어 있다. 이 파일이 dalforge-hub의 근간이며, 모든 도구는 이 스펙을 따른다.

## Contributing

기여 규칙은 [`CONTRIBUTING.md`](./CONTRIBUTING.md)에서 시작한다.
