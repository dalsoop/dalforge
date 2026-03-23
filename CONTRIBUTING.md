# Contributing

dalcenter는 dal(AI 인형)의 생명주기를 관리하는 런타임이다. dal 템플릿은 git으로, 런타임은 dalcenter가 담당한다.

## Ground Rules

1. 동작 변경은 테스트를 같이 넣는다.
2. 사용자-facing 동작이 바뀌면 README나 문서를 같은 변경에 포함한다.
3. `wake`, `sleep`, `sync`, `validate` 중 영향을 받는 경로를 최소 하나는 검증한다.

## Where To Start

- 제품 개요: [`README.md`](./README.md)
- 아키텍처: [`docs/architecture.md`](./docs/architecture.md)
- 스펙: [`dal.spec.cue`](./dal.spec.cue)
- 진입점: [`cmd/dalcenter/main.go`](./cmd/dalcenter/main.go)

## Project Structure

```
cmd/
  dalcenter/       운영자 CLI (serve, wake, sleep, sync, ...)
  dalcli/          팀원 dal용 CLI (status, ps, report)
  dalcli-leader/   팀장 dal용 CLI (wake, sleep, assign, ...)
internal/
  daemon/          HTTP 데몬 + Docker 관리 + soft-serve + Mattermost
  localdal/        dal/skill CRUD + CUE 검증
  talk/            Mattermost 봇 통신
  bridge/          Mattermost 메시지 브릿지
  vault/           시크릿 관리
dockerfiles/       player별 Docker 이미지
```

## Validation

```bash
go build ./...
go test ./...
```
