# dalcenter Docs

이 디렉터리는 `dalcenter`를 실제로 운영할 때 필요한 문서의 입구다.

Canonical home: `https://dalcenter.com`

## Start Here

- [`../README.md`](../README.md)
  - 제품 개요, 빠른 시작, 현재 되는 것
- [`runbooks/first-join-and-provision.md`](./runbooks/first-join-and-provision.md)
  - 첫 패키지를 등록하고 LXC를 생성/정리하는 최소 운영 흐름

## Mental Model

- `dalcenter`
  - 패키지와 스펙을 유통하고, 사용자 레포의 `.dal-template`를 읽어 등록, export, 실행, provision을 담당하는 self-hosted 허브
- 사용자 레포
  - `.dal-template/`를 가진 실제 프로젝트

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
- `.dal-template` discovery 규칙
- multi-runtime 운영 예시
