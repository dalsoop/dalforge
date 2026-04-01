#!/usr/bin/env bash
# setup-webhooks.sh — Mattermost outgoing webhook 자동 생성
#
# CT 내부의 Mattermost API를 사용하여 각 팀 채널에
# dalbridge로 메시지를 전달하는 outgoing webhook을 생성한다.
#
# Usage: ./setup-webhooks.sh [CT_ID]
#
# 환경변수:
#   MM_URL    — Mattermost URL (default: http://localhost:8065)
#   MM_TOKEN  — Mattermost admin token (필수)
#   MM_TEAM   — Team slug (default: prelik)

set -euo pipefail

CT_ID="${1:-202}"
MM_URL="${MM_URL:-http://localhost:8065}"
MM_TOKEN="${MM_TOKEN:-}"
MM_TEAM="${MM_TEAM:-prelik}"
DALBRIDGE_CALLBACK="${DALBRIDGE_CALLBACK:-http://localhost:4280/webhook}"

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

log() { echo -e "${GREEN}[webhooks]${NC} $*"; }
err() { echo -e "${RED}[webhooks]${NC} $*" >&2; }

# MM API 호출 (CT 내부에서 실행)
mm_api() {
    local method="$1" path="$2"
    shift 2
    pct exec "$CT_ID" -- curl -s -X "$method" \
        "${MM_URL}${path}" \
        -H "Authorization: Bearer ${MM_TOKEN}" \
        -H "Content-Type: application/json" \
        "$@"
}

# 팀 ID 조회
get_team_id() {
    mm_api GET "/api/v4/teams/name/${MM_TEAM}" | jq -r '.id'
}

# 채널 ID 조회
get_channel_id() {
    local team_id="$1" channel_name="$2"
    mm_api GET "/api/v4/teams/${team_id}/channels/name/${channel_name}" | jq -r '.id // empty'
}

# 기존 webhook 목록 조회
list_webhooks() {
    local team_id="$1"
    mm_api GET "/api/v4/hooks/outgoing?team_id=${team_id}&per_page=200"
}

# outgoing webhook 생성
create_webhook() {
    local team_id="$1"
    local channel_id="$2"
    local channel_name="$3"
    local display_name="dalbridge-${channel_name}"

    # 이미 존재하는지 확인
    local existing
    existing=$(list_webhooks "$team_id" | jq -r --arg name "$display_name" '.[] | select(.display_name == $name) | .id')
    if [ -n "$existing" ]; then
        log "webhook already exists for ${channel_name} (id: ${existing}), skipping"
        return
    fi

    local payload
    payload=$(jq -n \
        --arg team_id "$team_id" \
        --arg channel_id "$channel_id" \
        --arg display_name "$display_name" \
        --arg callback "$DALBRIDGE_CALLBACK" \
        --arg desc "dalbridge relay for #${channel_name}" \
        '{
            team_id: $team_id,
            channel_id: $channel_id,
            display_name: $display_name,
            description: $desc,
            callback_urls: [$callback],
            content_type: "application/json",
            trigger_when: 0
        }')

    local result
    result=$(mm_api POST "/api/v4/hooks/outgoing" -d "$payload")
    local webhook_id
    webhook_id=$(echo "$result" | jq -r '.id // empty')

    if [ -n "$webhook_id" ]; then
        log "created webhook for #${channel_name} (id: ${webhook_id})"
    else
        err "failed to create webhook for #${channel_name}: $(echo "$result" | jq -r '.message // .error // "unknown error"')"
    fi
}

main() {
    if [ -z "$MM_TOKEN" ]; then
        err "MM_TOKEN is required. Set it to a Mattermost admin token."
        err "  export MM_TOKEN=<your-admin-token>"
        exit 1
    fi

    log "Setting up outgoing webhooks (CT: ${CT_ID}, team: ${MM_TEAM})"

    local team_id
    team_id=$(get_team_id)
    if [ -z "$team_id" ] || [ "$team_id" = "null" ]; then
        err "Team '${MM_TEAM}' not found"
        exit 1
    fi
    log "Team ID: ${team_id}"

    # 각 팀 채널에 webhook 생성
    local channels=(dalcenter veilkey-v2 veilkey-selfhosted bridge-of-gaya-script dal-qa-team host)
    for ch in "${channels[@]}"; do
        local channel_id
        channel_id=$(get_channel_id "$team_id" "$ch")
        if [ -z "$channel_id" ]; then
            warn "channel #${ch} not found, skipping"
            continue
        fi
        create_webhook "$team_id" "$channel_id" "$ch"
    done

    log "Webhook setup complete"
}

main
