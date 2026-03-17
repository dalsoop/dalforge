# dalforge-hub

`dalforge`는 허브다. 사용자 레포 안의 `.dalfactory`를 읽고, 그 선언으로 `localdal` 실행 인스턴스를 만들고 관리한다.

## 지금 되는 것

`dalcenter`는 현재 `.dalfactory`를 실제 운영 흐름으로 연결한다.

- `.dalfactory` validate
- `join/list/status` 기반 localdal 등록 및 조회
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

즉 지금은 “설계 문서만 있는 상태”가 아니라, 파생 레포의 `.dalfactory`에서 localdal과 LXC까지 이어지는 초기 운영 버전이다.

## 한 줄 구조

- `dalforge`: 허브 레포. `dalcenter`, `dal`, 스펙, 문서, 운영 도구가 있다.
- 사용자 레포: 실제 프로젝트 레포. 여기에 `.dalfactory/`가 있다.
- `localdal`: 그 사용자 레포의 `.dalfactory`를 읽고 로컬에 만들어진 실행 인스턴스다.

한 줄로 줄이면:

`dalforge`는 읽고 관리한다. `.dalfactory`는 사용자 레포에 있다. `localdal`은 실행된다.

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

### dalforge / dalcenter (허브)

`dalforge`는 허브이고, `dalcenter`는 그 허브의 중앙 레지스트리다.

- 패키지(CLI/스킬/훅) 등록 및 버전 관리
- localdal 인스턴스 생성 및 상태 추적
- 시크릿(API 키 등) 암호화 저장 및 배포
- 노드별 설치 현황(인벤토리) 관리
- 감사 이벤트 기록

### localdal (실행 인스턴스)

실제로 생성된 dal 하나다. 사용자 레포의 `.dalfactory`를 기반으로 로컬에 만들어진 실행 인스턴스다. 안에는 CLI 도구, 스킬, 훅, 시크릿이 담겨 있고, 하나 이상의 PLAYER(에이전트)가 작업한다.

### .dalfactory (레포 선언)

사용자 레포 루트에 위치하는 폴더다. `dalforge` 허브 레포가 아니라, 실제 프로젝트 레포 안에 들어간다. 이 폴더가 localdal을 만들기 위한 설계도다.

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

localdal 안에서 실제로 일하는 주체. 하나의 dal에 여러 PLAYER가 있을 수 있다. 각 PLAYER는 서로 다른 에이전트(claude, codex, gemini)일 수 있고, 서로 다른 도구 세트를 가진다.

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

### 2. localdal 등록

```bash
dalcenter join /path/to/repo
```

현재 `join`은 아래를 수행한다.

1. 사용자 레포의 `.dalfactory/dal.cue` 읽기
2. manifest validate
3. skill/hook export
4. localdal instance dir 생성
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

dalcenter와 localdal은 주기적으로 상태를 동기화한다.

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

### 2. localdal 등록

```bash
dalcenter join /root/dalforge-hub/dalcli/dalcli-agent-coach
dalcenter list
dalcenter status dalcli-agent-coach
```

### 3. 로컬 실행 관리

`build.entry`가 있으면 `--command` 없이도 실행된다.

```bash
dalcenter start dalcli-agent-coach
dalcenter status dalcli-agent-coach
dalcenter stop dalcli-agent-coach
```

### 4. Proxmox LXC 생성

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

### 5. Proxmox LXC 제거

```bash
dalcenter destroy dalcli-agent-coach
```

### 6. dal과 연동

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

## 스펙

모든 규약은 dalcenter/dal.spec.cue 에 CUE로 정의되어 있다. 이 파일이 dalforge-hub의 근간이며, 모든 도구는 이 스펙을 따른다.
