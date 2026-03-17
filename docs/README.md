# dalforge Docs

이 디렉터리는 `dalforge`와 `dalcenter`를 실제로 운영할 때 필요한 문서의 입구다.

## Start Here

- [`../README.md`](../README.md)
  - 제품 개요, 빠른 시작, 현재 되는 것
- [`runbooks/first-join-and-provision.md`](./runbooks/first-join-and-provision.md)
  - 첫 패키지를 등록하고 LXC를 생성/정리하는 최소 운영 흐름

## Mental Model

- `dalforge`
  - 패키지와 스펙을 유통하는 클라우드 허브
- `dalcenter`
  - 사용자 레포의 `.dalfactory`를 읽고 등록, export, 실행, provision을 담당하는 관리 주체
- 사용자 레포
  - `.dalfactory/`를 가진 실제 프로젝트

## Core Commands

- `dalcenter catalog search <query>`
- `dalcenter join <repo-or-package>`
- `dalcenter list`
- `dalcenter status <name-or-id>`
- `dalcenter export <repo>`
- `dalcenter start <name> -c "<cmd>"`
- `dalcenter provision <name> --dry-run`
- `dalcenter destroy <name>`
- `dalcenter reconcile`
- `dalcenter watch --interval 60`

## What To Add Next

- gateway/runbook 문서
- healthcheck 표준 규약
- `.dalfactory` discovery 규칙
- multi-runtime 운영 예시
