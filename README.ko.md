# dalforge-hub

영문 문서: [`README.md`](./README.md)

`dalforge`는 `.dalfactory` 선언을 실제 로컬 실행과 LXC 운영으로 바꾸는 self-hosted orchestration stack입니다.

핵심 구조는 이렇습니다.

- `dalforge`
  - 패키지와 스펙을 유통하는 클라우드 허브
- `dalcenter`
  - `.dalfactory`를 읽고 등록, export, 실행, provision을 담당하는 관리 주체
- 사용자 레포
  - `.dalfactory/`를 가진 실제 프로젝트

즉:

- `.dalfactory`가 SSOT이고
- `dalforge`는 배포하고
- `dalcenter`는 실행하고 관리합니다

## 왜 필요한가

보통 에이전트 환경은 이런 식으로 무너지기 쉽습니다.

- 스킬, 훅, 런타임 설정이 레포마다 흩어짐
- 로컬 실행과 컨테이너 실행이 따로 놂
- 어떤 레포가 어떤 에이전트 환경을 요구하는지 재현이 어려움

`dalforge`는 이걸 `.dalfactory` 하나로 묶어서, 로컬 export와 Proxmox LXC까지 이어주는 쪽에 가깝습니다.

## 빠른 시작

가장 빠른 시작은 패키지를 찾고, 레포를 등록하고, 상태를 보는 것입니다.

```bash
dalcenter catalog search agent-coach
dalcenter join agent-coach
dalcenter list
dalcenter status agent-coach
```

## 성공했을 때 보이는 것

예를 들면 이런 흐름입니다.

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

즉 최소 성공 기준은:

- `list`에서 `STATUS=ready`
- `HEALTH=ok(...)`
- `status`에서 `source_type`, `source_ref`, `health_status` 확인

## 핵심 개념

- `dalforge`
  - npm registry 같은 상위 유통/배포 허브
- `dalcenter`
  - `.dalfactory`를 읽고 등록하고 상태를 관리하는 주체
- `.dalfactory`
  - 사용자 레포 안에 있는 실행/exports/container/agents 선언 SSOT

## 이게 아닌 것

- 단순 패키지 설치기만 있는 도구
- cloud SaaS control plane만 있는 제품
- `.dalfactory` 없이 수동 설정만 전제하는 런타임
- 대규모 멀티테넌트 운영 플랫폼 완성형

## 트레이드오프

- 단순 스크립트보다 구조가 크고 개념이 많습니다
- Proxmox/LXC까지 다루면 운영 난이도가 올라갑니다
- `.dalfactory` 선언을 유지해야 하는 대신 재현성과 관리성이 좋아집니다

## 대략적인 비교

| 도구 형태 | 기본 모델 | dalforge 차이점 |
|---|---|---|
| 단순 CLI 설치기 | 패키지 설치 후 끝 | `.dalfactory`를 읽고 등록, export, 상태, provision까지 연결 |
| 일반 task runner | 로컬 명령 실행 중심 | 레포 선언과 런타임 상태를 함께 관리 |
| 컨테이너 provision 도구 | 인프라 생성 중심 | skill/hook export와 로컬 실행 흐름까지 같이 다룸 |

## 기여

기여 규칙은 여기서 시작합니다.

- [`CONTRIBUTING.md`](./CONTRIBUTING.md)
