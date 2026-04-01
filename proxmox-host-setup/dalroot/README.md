# dalroot 알림 인프라

dalroot(호스트 Claude Code)가 Mattermost 메시지를 실시간 수신하기 위한 인프라.

## 아키텍처

```
Mattermost (#dalcenter, #host, ...)
    │
    ▼ outgoing webhook
CT 202: dalbridge (:4280/webhook)
    │
    ▼ SSE stream (:4280/stream)
Host: dalroot-listener
    │
    ▼ file write
/var/lib/dalroot/inbox/{dalroot-id}/*.msg
    │
    ▼ Claude Code hook (UserPromptSubmit)
dalroot-check-notifications → stdout
```

## 구성요소

### 호스트 스크립트 (/usr/local/bin/)

| 스크립트 | 설명 |
|---------|------|
| `dalroot-id` | tmux pane 역추적으로 `dalroot-{session}-{window}-{pane}` ID 생성 |
| `dalroot-check-notifications` | inbox에서 unread 알림 읽기 + 삭제 |
| `dalroot-register` | SessionStart hook — inbox 생성 + MM 등록 |
| `dalroot-task` | dalcenter task 래퍼 (팀 라우팅 + callback) |
| `dalroot-listener` | dalbridge SSE stream 구독 → inbox 전달 |

### CT 202 (Mattermost LXC)

| 구성요소 | 설명 |
|---------|------|
| `dalbridge` | MM outgoing webhook → SSE stream 릴레이 |
| outgoing webhooks | 각 팀 채널 → dalbridge:4280 |

### systemd 서비스

| 서비스 | 위치 | 설명 |
|--------|------|------|
| `dalroot-listener.service` | Host | listener 데몬 |
| `dalbridge.service` | CT 202 | dalbridge 데몬 |

## 설치

```bash
# 전체 설치 (호스트 + CT 202 dalbridge + webhooks)
./install.sh

# 호스트만
./install.sh --host

# dalbridge만 (CT ID 지정 가능)
./install.sh --bridge 202

# webhook만
MM_TOKEN=<admin-token> ./install.sh --webhooks 202
```

## Claude Code Hooks

`~/.claude/settings.json`에 추가:

```json
{
  "hooks": {
    "SessionStart": [{"type": "command", "command": "dalroot-register"}],
    "UserPromptSubmit": [{"type": "command", "command": "dalroot-check-notifications"}],
    "Notification": [{"type": "command", "command": "dalroot-check-notifications"}]
  }
}
```
