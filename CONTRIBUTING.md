# Contributing

`dalcenter`는 `.dal-template` 선언을 실제 운영 흐름으로 연결하는 도구다.

그래서 기여는 “코드가 돌아간다”만으로 충분하지 않고, 선언과 실제 동작이 함께 맞아야 한다.

## Ground Rules

1. 동작 변경은 회귀 테스트를 같이 넣는다.
2. 사용자-facing 동작이 바뀌면 README나 문서를 같은 변경에 포함한다.
3. `validate`, `join`, `export`, `start/stop`, `provision/destroy` 중 영향을 받는 실제 경로를 최소 하나는 검증한다.
4. `.dal-template` 계약을 바꾸는 변경은 예시나 검증 경로도 같이 갱신한다.

## Where To Start

- 제품 개요: [`README.md`](./README.md)
- 스펙: [`dal.spec.cue`](./dal.spec.cue)
- 실행 진입점: [`cmd/dalcenter/main.go`](./cmd/dalcenter/main.go)

## Common Contribution Areas

- `.dal-template` 검증 강화
- skill/hook export 개선
- catalog / cloud source 흐름 개선
- start/stop/reconcile/watch UX 개선
- Proxmox provision 운영성 개선
- 문서와 예제 manifest 보강

## Minimum Validation

기본 검증:

```bash
go test ./...
```

기능이 바뀌면 가능한 범위에서 실제 CLI 경로도 확인한다.

예:

```bash
go run ./cmd/dalcenter catalog search agent-coach
go run ./cmd/dalcenter join agent-coach
go run ./cmd/dalcenter list
go run ./cmd/dalcenter status agent-coach
```

## Pull Request Standard

좋은 변경은 네 가지가 바로 보여야 한다.

- 무엇이 바뀌었는지
- 왜 바꿨는지
- 어떤 명령이나 테스트로 검증했는지
- 사용자나 운영자 입장에서 뭐가 달라지는지
