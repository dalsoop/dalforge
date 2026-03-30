---
id: DAL:SKILL:cred0001
---
> **CT_ID**: dalcenter LXC의 VMID. 현재 기본값 `105`. 이중화 시 `125`.

# Credential Ops — 토큰 관리

## 아키텍처

```
호스트 (PVE)
  ~/.claude/.credentials.json    ← Claude Code 자동 갱신
  ~/.codex/auth.json             ← Codex 자동 갱신
  dal-credential-sync (timer, 30분)
    → pve-sync-creds $CT (직접 복사)

LXC $CT (dalcenter)
  ~/.claude/.credentials.json    ← 호스트에서 직접 복사됨
  bind-mount → 모든 Docker 컨테이너 즉시 반영
  cred-watcher (5분) → 만료 임박 시 credential-ops로 sync 요청
```

## 진단

### 토큰 만료 확인
```bash
# 호스트
python3 -c "
import json, datetime
with open('/root/.claude/.credentials.json') as f:
    d = json.load(f)
exp = d['claudeAiOauth']['expiresAt']
exp_dt = datetime.datetime.fromtimestamp(exp/1000)
print(f'만료: {exp_dt}, 남은시간: {exp_dt - datetime.datetime.now()}')
"

# LXC $CT (UTC)
pct exec $CT -- python3 -c "
import json, datetime
with open('/root/.claude/.credentials.json') as f:
    d = json.load(f)
exp = d['claudeAiOauth']['expiresAt']
exp_dt = datetime.datetime.fromtimestamp(exp/1000)
print(f'만료: {exp_dt}, 남은시간: {exp_dt - datetime.datetime.now()}')
"
```

### auth error 확인
```bash
pct exec $CT -- bash -c '
for c in $(docker ps --format "{{.Names}}"); do
  errs=$(docker logs --since 1h "$c" 2>&1 | grep -i "auth error" | tail -1)
  [ -n "$errs" ] && echo "$c: $errs"
done
'
```

## 복구

### 수동 토큰 갱신
```bash
# 1. 호스트 토큰 refresh
proxmox-host-setup ai sync --agent claude

# 2. LXC $CT로 직접 복사
pve-sync-creds $CT

# 또는 한 번에
dal-credential-sync
```

### cred-watcher 로그 확인
```bash
pct exec $CT -- journalctl -u dalcenter@dalcenter --since "30 min ago" --no-pager | grep "cred-watcher"
```

### 자동 갱신이 안 될 때
1. 호스트 timer 확인: `systemctl status dal-credential-sync.timer`
2. 수동 실행: `dal-credential-sync`
3. 직접 복사: `pve-sync-creds $CT`
