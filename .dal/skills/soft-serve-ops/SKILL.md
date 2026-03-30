---
id: DAL:SKILL:soft0001
---
> **CT_ID**: dalcenter LXC의 VMID. 현재 기본값 `105`. 이중화 시 `125`.

# Soft-Serve Ops — 로컬 Git 서버 관리

## 위치
- LXC $CT, PID 확인: `pct exec $CT -- pgrep -a soft`
- 데이터: `$DALCENTER_DATA_DIR/soft-serve/`
- SSH: `:23231`, HTTP: `:23232`, Git: `:9418`, Stats: `localhost:23233`

## 시작/중지

soft-serve는 dalcenter serve가 시작할 때 자동으로 뜸. 수동 관리 필요 시:

```bash
# 중지
pct exec $CT -- kill $(pgrep -f "soft serve")

# 시작 (INITIAL_ADMIN_KEYS 필요)
pct exec $CT -- bash -c '
export SOFT_SERVE_DATA_PATH="$DALCENTER_DATA_DIR/soft-serve"
export SOFT_SERVE_INITIAL_ADMIN_KEYS="$(cat /root/.ssh/id_ed25519.pub)"
nohup /usr/local/bin/soft serve > /tmp/soft-serve.log 2>&1 &
'
```

## 레포 관리

```bash
# 레포 목록
pct exec $CT -- ssh -o StrictHostKeyChecking=no -p 23231 localhost repo list

# 레포 생성
pct exec $CT -- ssh -o StrictHostKeyChecking=no -p 23231 localhost repo create <name>

# 유저 목록
pct exec $CT -- ssh -o StrictHostKeyChecking=no -p 23231 localhost user list

# 유저 추가 + SSH 키 등록
pct exec $CT -- ssh -p 23231 localhost user create <username>
pct exec $CT -- ssh -p 23231 localhost user add-pubkey <username> '<ssh-pubkey>'

# collaborator 추가
pct exec $CT -- ssh -p 23231 localhost repo collab add <repo> <username>
```

## DB 리셋 (인증 부트스트랩)

유저 DB 깨졌을 때:
```bash
pct exec $CT -- bash -c '
kill $(pgrep -f "soft serve") 2>/dev/null; sleep 1
rm $DALCENTER_DATA_DIR/soft-serve/soft-serve.db
export SOFT_SERVE_DATA_PATH="$DALCENTER_DATA_DIR/soft-serve"
export SOFT_SERVE_INITIAL_ADMIN_KEYS="$(cat /root/.ssh/id_ed25519.pub)"
nohup /usr/local/bin/soft serve > /tmp/soft-serve.log 2>&1 &
sleep 2
# 호스트 키 재등록 필요
'
```

## 현재 레포
- `dal-credentials` — credential sync용 (호스트 ↔ LXC)
