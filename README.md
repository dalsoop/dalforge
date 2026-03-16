# dalforge-hub

dal은 AI 에이전트가 빙의된 사용자의 말이다. dalforge-hub는 이 dal을 만들고, 관리하고, 실행하는 대장간이다.

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

### dalcenter (중앙)

모든 dal을 관리하는 중앙 레지스트리다.

- 패키지(CLI/스킬/훅) 등록 및 버전 관리
- localdal 인스턴스 생성 및 상태 추적
- 시크릿(API 키 등) 암호화 저장 및 배포
- 노드별 설치 현황(인벤토리) 관리
- 감사 이벤트 기록

### localdal (인형 인스턴스)

실제로 생성된 dal 하나. .dalfactory 템플릿을 기반으로 만들어진다. 안에는 CLI 도구, 스킬, 훅, 시크릿이 담겨 있고, 하나 이상의 PLAYER(에이전트)가 작업한다.

### .dalfactory (인형 공장)

레포 루트에 위치하는 폴더. dal을 만들기 위한 설계도(template)가 여러 개 들어있다.

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

### 2. 인스턴스 생성

```bash
dalcenter join claude-dev
```

1. .dalfactory/templates/claude-dev.cue 읽음
2. 컨테이너 생성 (base image + packages + agents 설치)
3. SecretVault 초기화 (API 키 암호화 저장)
4. CLI/스킬/훅 설치
5. PLAYER(에이전트) 시작

같은 템플릿으로 여러 인스턴스 생성 가능:

```bash
dalcenter join claude-dev --name "feature-a"
dalcenter join claude-dev --name "feature-b"
dalcenter join codex-worker --name "background-task"
```

### 3. 빌드 및 Export

.dalfactory/ 가 소스(SSOT)이고, 각 에이전트용 설정은 빌드 산출물로 export된다.

1차 규칙은 레포 루트의 `skills/{name}/SKILL.md` 같은 원본 자산을 직접 export하는 것이다. 예외적으로 원본 허브 성격의 레포는 `source/document/skills/{name}/SKILL.md` 경로를 fallback export source로 선언할 수 있다.

```
.dalfactory/ (소스)
    -> build
.claude/     (claude용 export)
    skills/
    hooks/
.codex/      (codex용 export)
    skills/
    hooks/
```

watch가 .dalfactory/ 변경을 감지하면 자동으로 빌드 -> export -> 반영.

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

## 스펙

모든 규약은 dalcenter/dal.spec.cue 에 CUE로 정의되어 있다. 이 파일이 dalforge-hub의 근간이며, 모든 도구는 이 스펙을 따른다.
