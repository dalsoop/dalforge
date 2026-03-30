---
id: DAL:SKILL:life0001
---
> **CT_ID**: dalcenter LXC의 VMID. 현재 기본값 `105`. 이중화 시 `125`.

# Dal Lifecycle — dal 생명주기 관리

## 포트 매핑

| 레포 | 포트 | systemd |
|------|------|---------|
| veilkey-selfhosted | 11190 | dalcenter@veilkey-selfhosted |
| bridge-of-gaya-script | 11191 | dalcenter@bridge-of-gaya-script |
| dalcenter | 11192 | dalcenter@dalcenter |
| veilkey-v2 | 11193 | dalcenter@veilkey-v2 |
| dal-qa-team | 11194 | dalcenter@dal-qa-team |
| emotion-ai | 11195 | 수동 (nsenter) |

## Wake / Sleep

```bash
# API 방식
pct exec $CT -- curl -s -X POST http://localhost:<port>/api/wake/<dal-name>
pct exec $CT -- curl -s -X POST http://localhost:<port>/api/sleep/<dal-name>

# dalcenter tell 방식 (leader에게 지시)
pct exec $CT -- dalcenter tell <repo> "dev 깨워"
pct exec $CT -- dalcenter tell <repo> "전부 재워"
```

## 상태 확인

```bash
# 실행 중인 dal 목록
pct exec $CT -- curl -s http://localhost:<port>/api/ps

# 전체 dal 상태 (sleeping 포함)
pct exec $CT -- curl -s http://localhost:<port>/api/status

# 특정 dal 로그
pct exec $CT -- docker logs --since 30m <container-name> 2>&1 | tail -20

# 컨테이너 진입
pct exec $CT -- docker exec -it <container-name> bash
```

## dal.cue 수정 후

dal.cue 변경 (player, skills 등)은 git push 후 자동 sync됨.
구조적 변경(skills 추가/삭제, image 변경)은 컨테이너 재생성 필요:

```bash
# sleep 후 wake로 재생성
pct exec $CT -- curl -s -X POST http://localhost:<port>/api/sleep/<dal-name>
pct exec $CT -- curl -s -X POST http://localhost:<port>/api/wake/<dal-name>
```

## Docker 이미지

```bash
# 현재 이미지 목록
pct exec $CT -- docker images | grep dalcenter

# 이미지 빌드
pct exec $CT -- dalcenter image build

# 이미지 종류
#   dalcenter/claude:latest  — Claude Code + Node.js
#   dalcenter/claude:go      — Claude Code + Go toolchain
#   dalcenter/claude:rust    — Claude Code + Rust toolchain
#   dalcenter/codex:latest   — Codex CLI
```

## env 파일

각 레포의 환경변수: `/etc/dalcenter/<repo>.env`
```bash
pct exec $CT -- cat /etc/dalcenter/<repo>.env
```
