# First Join And Provision

이 runbook은 `dalforge`에서 패키지를 찾고, `dalcenter`에 등록하고, Proxmox LXC까지 만드는 가장 짧은 흐름이다.

## 1. 패키지 찾기

```bash
dalcenter catalog search agent-coach
```

성공 기준:

- 원하는 패키지가 목록에 보인다

## 2. 인스턴스 등록

```bash
dalcenter join agent-coach
dalcenter list
dalcenter status agent-coach
```

성공 기준:

- `instance created: dal-...`
- `health check: ok`
- `list`에서 `STATUS=ready`

## 3. export 확인

Claude 또는 Codex skill export가 붙는 패키지라면 아래를 확인한다.

```bash
ls ~/.claude/skills
ls ~/.codex/skills
```

성공 기준:

- 선언된 skill 이름이 symlink로 보인다

## 4. LXC dry-run

```bash
dalcenter provision agent-coach --dry-run --vmid 219318 --storage local-lvm --bridge vmbr0 --memory 512 --cores 1
```

성공 기준:

- `pct create`
- `pct start`
- 필요 시 `apt-get update`
- 필요 시 `apt-get install`
  순서가 출력된다

## 5. 실제 provision

```bash
dalcenter provision agent-coach --vmid 219318 --storage local-lvm --bridge vmbr0 --memory 512 --cores 1
```

성공 기준:

- `pct status 219318`가 `running`
- 컨테이너 내부 패키지가 설치되어 있다

## 6. 정리

```bash
dalcenter destroy agent-coach
```

성공 기준:

- 컨테이너가 제거된다
- `status`에서 provision 상태가 정리된다

## Notes

- `dal dismiss <pkg>`는 `unexport`와 `destroy`를 soft dependency로 자동 호출한다
- 실제 운영 전에는 `reconcile`과 `watch`를 함께 붙여 drift를 점검하는 편이 안전하다
