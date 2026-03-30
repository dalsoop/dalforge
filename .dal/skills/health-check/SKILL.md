---
id: DAL:SKILL:hlth0001
---
> **CT_ID**: dalcenter LXC의 VMID. 현재 기본값 `105`. 이중화 시 `125`.

# Health Check — 시스템 헬스체크

## 전체 상태 확인

```bash
# 1. LXC 상태
pct list | grep -E "dalcenter|soulflow|traefik|veilkey"

# 2. dalcenter 서비스 상태
pct exec $CT -- systemctl is-active \
  dalcenter@dalcenter \
  dalcenter@veilkey-selfhosted \
  dalcenter@veilkey-v2 \
  dalcenter@bridge-of-gaya-script \
  dalcenter@dal-qa-team

# 3. Docker 컨테이너
pct exec $CT -- docker ps --format "table {{.Names}}\t{{.Status}}"

# 4. API 응답
pct exec $CT -- bash -c '
for port in 11190 11191 11192 11193 11194; do
  status=$(curl -s --max-time 2 http://localhost:$port/api/health)
  echo ":$port $status"
done
'
```

## dal 상태 요약

```bash
pct exec $CT -- bash -c '
for port in 11190 11191 11192 11193 11194; do
  curl -s http://localhost:$port/api/status 2>/dev/null | python3 -c "
import sys,json
data = json.load(sys.stdin)
if data:
    path = data[0][\"Path\"]
    repo = path.split(\"/\")[-3] if \".dal\" in path else \"?\"
    print(f\"=== {repo} ===\")
    for d in data:
        print(f\"  {d[\"Name\"]:20s} {d[\"Role\"]:8s} {d[\"Player\"]:8s} {d[\"container_status\"]}\")
" 2>/dev/null
done
'
```

## 에러 로그 스캔

```bash
pct exec $CT -- bash -c '
for c in $(docker ps --format "{{.Names}}"); do
  errs=$(docker logs --since 1h "$c" 2>&1 | grep -iE "error|fatal|panic" | tail -3)
  if [ -n "$errs" ]; then
    echo "--- $c ---"
    echo "$errs"
  fi
done
'
```

## 리소스 사용량

```bash
pct exec $CT -- docker stats --no-stream --format "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}"
```

## credential 상태

```bash
# 호스트 토큰
python3 -c "
import json, datetime
with open('/root/.claude/.credentials.json') as f:
    d = json.load(f)
exp = d['claudeAiOauth']['expiresAt']
remaining = (exp/1000) - datetime.datetime.now().timestamp()
print(f'호스트: {int(remaining/3600)}h {int(remaining%3600/60)}m 남음')
"

# LXC $CT 토큰
pct exec $CT -- python3 -c "
import json, datetime
with open('/root/.claude/.credentials.json') as f:
    d = json.load(f)
exp = d['claudeAiOauth']['expiresAt']
remaining = (exp/1000) - datetime.datetime.now().timestamp()
print(f'LXC $CT: {int(remaining/3600)}h {int(remaining%3600/60)}m 남음')
"

# sync timer
systemctl status dal-credential-sync.timer --no-pager | head -5
```

## soft-serve 상태

```bash
pct exec $CT -- bash -c '
pgrep -a soft || echo "soft-serve NOT RUNNING"
ssh -o StrictHostKeyChecking=no -p 23231 localhost info 2>&1 | head -3
'
```
