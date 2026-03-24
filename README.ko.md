<div align="center">
  <h1>dalcenter</h1>
  <p><strong>Dal 생명주기 관리자 — AI 에이전트 인스턴스를 생성, 프로비저닝, 동기화</strong></p>
  <p>
    <a href="https://github.com/dalsoop/dalcenter"><img src="https://img.shields.io/badge/github-dalsoop%2Fdalcenter-181717?logo=github&logoColor=white" alt="GitHub repository"></a>
    <a href="./LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-2563eb.svg" alt="AGPL-3.0 License"></a>
  </p>
  <p><a href="./README.md">English</a></p>
</div>

dalcenter는 dal(AI 에이전트)을 관리합니다. `.dalfactory` 매니페스트로 정의된 인스턴스를 스킬, 시크릿, 헬스체크와 함께 관리합니다. 패키지는 dalforge cloud 또는 로컬 소스에서 가져오고, dalcenter가 생명주기를 담당합니다.

## 빠른 시작

```bash
# 1. API 서버 시작
dalcenter serve --port 10100

# 2. dal 패키지 검색 및 참여
dalcenter catalog search agent-coach
dalcenter join dalcli-agent-coach

# 3. 매니페스트 검증
dalcenter validate

# 4. 프로비저닝 및 시작
dalcenter provision <name>
dalcenter start <name>
dalcenter list

# 5. 작업 끝
dalcenter stop <name>
```

## 동작 방식

```
.dalfactory/ (매니페스트)
  dal.cue                            ← dal 정의 + 템플릿

dalcenter join <source>
  → 패키지 소스 다운로드/클론
  → ~/.dalcenter/instances/에 인스턴스 생성

dalcenter provision <name>
  → .dalfactory/dal.cue 읽기
  → LXC 컨테이너 프로비저닝 (bridge, cores, memory, storage)

dalcenter start <name>
  → 매니페스트의 build.entry 실행
  → Claude/Codex에 스킬 export
  → 헬스체크 시작

dalcenter reconcile
  → 모든 인스턴스 점검
  → export 복구 (스킬, 훅, 설정)
```

## 구조

```
LXC: dalcenter
├── dalcenter serve          API 서버 (포트 10100)
├── dalcenter watch          지속적 reconcile 루프
├── instance: leader         dalcli-leader 내장
├── instance: dev            dalcli 내장
└── instance: dev-2          복수 인스턴스 지원
```

## CLI

```
dalcenter serve                          # API 서버 (dal 레지스트리)
dalcenter join <source>                  # .dalfactory 매니페스트로 인스턴스 생성
dalcenter provision <name>               # LXC 컨테이너 프로비저닝
dalcenter start <name>                   # 인스턴스 프로세스 시작
dalcenter stop <name>                    # 프로세스 정지
dalcenter restart <name>                 # 프로세스 재시작
dalcenter destroy <name>                 # 컨테이너 정지 및 삭제
dalcenter list                           # 모든 인스턴스 목록
dalcenter status [name]                  # 인스턴스 상태 확인
dalcenter validate [path...]             # CUE 스키마 검증
dalcenter reconcile                      # 모든 export 점검 및 복구
dalcenter watch                          # 지속적 reconcile (기본 60초)
dalcenter export [path...]               # 매니페스트에서 Claude 스킬 export
dalcenter unexport [path...]             # export된 스킬 제거
dalcenter secret set <name>              # 시크릿 저장 (stdin에서 읽기)
dalcenter secret get <name>              # 시크릿 조회
dalcenter secret list                    # 시크릿 목록
dalcenter catalog search [query]         # dalforge cloud 패키지 검색
dalcenter talk run                       # dal 통신 데몬 (MM 브릿지)
dalcenter talk conductor                 # 중앙 오케스트레이터 봇
dalcenter talk setup                     # Mattermost 봇 계정 생성
dalcenter talk teardown                  # 봇 계정 비활성화
dalcenter tui                            # 터미널 대시보드
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

## .dalfactory/dal.cue

```cue
schema_version: "1.0.0"

dal: {
    id:       "DAL:CLI:848a4292"
    name:     "dalcli-agent-coach"
    version:  "0.1.0"
    category: "CLI"
}

description: "AI agent coaching CLI tool"

templates: default: {
    schema_version: "1.0.0"
    name:           "default"
    description:    "Default runtime"
    container: {
        base:     "ubuntu:24.04"
        packages: ["bash", "python3"]
        agents: {}
    }
    permissions: {
        filesystem: ["/tmp/dal-*"]
        network:    false
    }
    build: {
        language: "python"
        entry:    "bin/app"
        output:   "bin/app"
    }
    health_check: {
        command: "bin/app status"
    }
    exports: claude: {
        skills: ["skills/my-skill/SKILL.md"]
    }
}
```

## 통신

dal 간 통신은 Mattermost. `dalcenter talk`으로 봇 계정과 메시지 라우팅을 관리합니다.

```bash
# dal용 봇 설정
dalcenter talk setup --url http://mm:8065 --username dal-dev \
  --login admin@example.com --password pass --channel project

# 통신 데몬 실행
dalcenter talk run --url http://mm:8065 --bot-token TOKEN \
  --bot-username dal-dev --channel-id CHID

# 중앙 오케스트레이터 실행
dalcenter talk conductor --url http://mm:8065 --bot-token TOKEN \
  --channel-id CHID --dal dev:member --dal leader:leader
```

- `dalcli-leader assign dev "작업"` → `@dal-dev 작업` 전송
- `dalcli report "완료"` → `[dev] 완료` 전송

## 기여

[`CONTRIBUTING.md`](./CONTRIBUTING.md) 참고.
