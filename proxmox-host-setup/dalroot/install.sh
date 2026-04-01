#!/usr/bin/env bash
# install.sh — dalroot 알림 인프라 설치 자동화
#
# Proxmox 호스트에서 실행:
#   ./install.sh [--host] [--bridge CT_ID]
#
# --host:  호스트에 dalroot 스크립트 + listener 설치
# --bridge CT_ID: CT에 dalbridge 설치 (default: 202)
# 인자 없으면 둘 다 설치

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DALCENTER_REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[install]${NC} $*"; }
warn() { echo -e "${YELLOW}[install]${NC} $*"; }
err()  { echo -e "${RED}[install]${NC} $*" >&2; }

# ─── 호스트 설치 ───────────────────────────────────────────────────

install_host() {
    log "Installing dalroot scripts to /usr/local/bin/..."

    local scripts=(dalroot-id dalroot-check-notifications dalroot-register dalroot-task dalroot-listener)
    for s in "${scripts[@]}"; do
        cp "$SCRIPT_DIR/$s" "/usr/local/bin/$s"
        chmod +x "/usr/local/bin/$s"
        log "  installed $s"
    done

    # inbox 디렉토리 생성
    mkdir -p /var/lib/dalroot/inbox
    log "  created /var/lib/dalroot/inbox"

    # systemd 서비스 설치
    log "Installing dalroot-listener.service..."
    cp "$SCRIPT_DIR/dalroot-listener.service" /etc/systemd/system/
    systemctl daemon-reload
    systemctl enable dalroot-listener.service
    systemctl restart dalroot-listener.service
    log "  dalroot-listener.service enabled and started"

    # Claude Code hooks 설정 안내
    warn ""
    warn "Claude Code settings.json에 다음 hooks를 추가하세요:"
    warn ""
    warn '  "hooks": {'
    warn '    "SessionStart": [{"type": "command", "command": "dalroot-register"}],'
    warn '    "UserPromptSubmit": [{"type": "command", "command": "dalroot-check-notifications"}],'
    warn '    "Notification": [{"type": "command", "command": "dalroot-check-notifications"}]'
    warn '  }'
    warn ""
}

# ─── dalbridge 설치 (CT 내부) ──────────────────────────────────────

install_bridge() {
    local ct_id="${1:-202}"

    log "Building dalbridge..."
    (cd "$DALCENTER_REPO" && GOOS=linux GOARCH=amd64 go build -o dist/dalbridge ./cmd/dalbridge/)
    log "  built dist/dalbridge"

    log "Installing dalbridge to CT ${ct_id}..."
    pct push "$ct_id" "$DALCENTER_REPO/dist/dalbridge" /usr/local/bin/dalbridge
    pct exec "$ct_id" -- chmod +x /usr/local/bin/dalbridge

    # systemd 서비스 설치
    pct push "$ct_id" "$SCRIPT_DIR/dalbridge.service" /etc/systemd/system/dalbridge.service
    pct exec "$ct_id" -- systemctl daemon-reload
    pct exec "$ct_id" -- systemctl enable dalbridge.service
    pct exec "$ct_id" -- systemctl restart dalbridge.service
    log "  dalbridge.service enabled and started on CT ${ct_id}"
}

# ─── Outgoing Webhook 생성 ─────────────────────────────────────────

setup_webhooks() {
    local ct_id="${1:-202}"

    log "Setting up outgoing webhooks..."
    if [ ! -f "$SCRIPT_DIR/setup-webhooks.sh" ]; then
        warn "setup-webhooks.sh not found, skipping webhook setup"
        return
    fi
    bash "$SCRIPT_DIR/setup-webhooks.sh" "$ct_id"
}

# ─── main ──────────────────────────────────────────────────────────

usage() {
    echo "Usage: $0 [--host] [--bridge [CT_ID]] [--webhooks [CT_ID]]"
    echo ""
    echo "Options:"
    echo "  --host              Install dalroot scripts + listener on host"
    echo "  --bridge [CT_ID]    Install dalbridge on CT (default: 202)"
    echo "  --webhooks [CT_ID]  Create MM outgoing webhooks (default: 202)"
    echo ""
    echo "No arguments: install everything (host + bridge CT 202 + webhooks)"
}

main() {
    if [ $# -eq 0 ]; then
        install_host
        install_bridge 202
        setup_webhooks 202
        log "All done!"
        exit 0
    fi

    while [ $# -gt 0 ]; do
        case "$1" in
            --host)
                install_host
                shift
                ;;
            --bridge)
                local ct="${2:-202}"
                [[ "$ct" == --* ]] && ct="202" || shift
                install_bridge "$ct"
                shift
                ;;
            --webhooks)
                local ct="${2:-202}"
                [[ "$ct" == --* ]] && ct="202" || shift
                setup_webhooks "$ct"
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                err "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done

    log "Done!"
}

main "$@"
