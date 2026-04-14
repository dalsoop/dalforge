#!/usr/bin/env bash
# ⚠️  이 스크립트의 실행으로 발생하는 모든 결과에 대한 귀책사유는 실행자 본인에게 있습니다.
#
# install.sh — HeyForm LXC 컨테이너 설치 자동화
#
# Proxmox VE 호스트에서 실행:
#   CTID=50180 CT_IP=10.0.0.180/24 CT_GW=10.0.0.1 ./install.sh
#
# 환경변수:
#   CTID          - 컨테이너 ID (필수)
#   CT_HOSTNAME   - 호스트명 (default: heyform)
#   CT_IP         - IP/CIDR (필수, e.g. 10.0.0.180/24)
#   CT_GW         - 게이트웨이 (필수, e.g. 10.0.0.1)
#   CT_MEMORY     - 메모리 MB (default: 2048)
#   CT_CORES      - CPU 코어 (default: 2)
#   CT_DISK       - 디스크 GB (default: 16)
#   CT_STORAGE    - 스토리지 (default: local-lvm)
#   CT_BRIDGE     - 네트워크 브릿지 (default: vmbr0)
#   CT_NAMESERVER - DNS (default: 8.8.8.8)
#   CT_TEMPLATE   - OS 템플릿 (default: debian-12-standard)
#   HEYFORM_PORT  - HeyForm 포트 (default: 9513)
#   APP_HOMEPAGE_URL - HeyForm 접속 URL (default: http://<CT_IP>:9513)

set -euo pipefail

# ─── 색상 ─────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log()  { echo -e "${GREEN}[heyform]${NC} $*"; }
info() { echo -e "${BLUE}[heyform]${NC} $*"; }
warn() { echo -e "${YELLOW}[heyform]${NC} $*"; }
err()  { echo -e "${RED}[heyform]${NC} $*" >&2; }

# ─── 환경변수 기본값 ──────────────────────────────────────────────────
CT_HOSTNAME="${CT_HOSTNAME:-heyform}"
CT_MEMORY="${CT_MEMORY:-2048}"
CT_CORES="${CT_CORES:-2}"
CT_DISK="${CT_DISK:-16}"
CT_STORAGE="${CT_STORAGE:-local-lvm}"
CT_BRIDGE="${CT_BRIDGE:-vmbr0}"
CT_NAMESERVER="${CT_NAMESERVER:-8.8.8.8}"
CT_TEMPLATE="${CT_TEMPLATE:-debian-12-standard}"
HEYFORM_PORT="${HEYFORM_PORT:-9513}"

# ─── 입력 검증 ────────────────────────────────────────────────────────
validate_inputs() {
    local errors=0

    if [ -z "${CTID:-}" ]; then
        err "CTID가 설정되지 않았습니다. (예: CTID=50180)"
        errors=$((errors + 1))
    fi

    if [ -z "${CT_IP:-}" ]; then
        err "CT_IP가 설정되지 않았습니다. (예: CT_IP=10.0.0.180/24)"
        errors=$((errors + 1))
    fi

    if [ -z "${CT_GW:-}" ]; then
        err "CT_GW가 설정되지 않았습니다. (예: CT_GW=10.0.0.1)"
        errors=$((errors + 1))
    fi

    if [ $errors -gt 0 ]; then
        echo ""
        err "사용법: CTID=50180 CT_IP=10.0.0.180/24 CT_GW=10.0.0.1 $0"
        exit 1
    fi

    # CTID가 이미 사용 중인지 확인
    if pct status "$CTID" &>/dev/null; then
        err "CTID $CTID 는 이미 존재합니다."
        exit 1
    fi

    # IP에서 CIDR 제거한 순수 IP 추출
    CT_IP_BARE="${CT_IP%%/*}"
    APP_HOMEPAGE_URL="${APP_HOMEPAGE_URL:-http://${CT_IP_BARE}:${HEYFORM_PORT}}"
}

# ─── [1/6] 템플릿 다운로드 ────────────────────────────────────────────
download_template() {
    log "[1/6] OS 템플릿 확인 중..."

    local template_path
    template_path=$(pveam list local 2>/dev/null | grep "$CT_TEMPLATE" | awk '{print $1}' | head -1)

    if [ -z "$template_path" ]; then
        info "템플릿 다운로드 중: ${CT_TEMPLATE}..."
        pveam update >/dev/null 2>&1
        local available
        available=$(pveam available --section system 2>/dev/null | grep "$CT_TEMPLATE" | awk '{print $2}' | sort -V | tail -1)
        if [ -z "$available" ]; then
            err "템플릿 '$CT_TEMPLATE'을 찾을 수 없습니다."
            exit 1
        fi
        pveam download local "$available"
        template_path="local:vztmpl/${available}"
    fi

    TEMPLATE_PATH="$template_path"
    log "  템플릿: ${TEMPLATE_PATH}"
}

# ─── [2/6] 컨테이너 생성 ──────────────────────────────────────────────
create_container() {
    log "[2/6] LXC 컨테이너 생성 (CTID: ${CTID})..."

    pct create "$CTID" "$TEMPLATE_PATH" \
        --hostname "$CT_HOSTNAME" \
        --memory "$CT_MEMORY" \
        --cores "$CT_CORES" \
        --rootfs "${CT_STORAGE}:${CT_DISK}" \
        --net0 "name=eth0,bridge=${CT_BRIDGE},ip=${CT_IP},gw=${CT_GW}" \
        --nameserver "$CT_NAMESERVER" \
        --unprivileged 0 \
        --features nesting=1 \
        --start 1 \
        --onboot 1

    # 컨테이너 시작 대기
    info "  컨테이너 부팅 대기 중..."
    local wait_count=0
    while ! pct exec "$CTID" -- true &>/dev/null; do
        sleep 2
        wait_count=$((wait_count + 1))
        if [ $wait_count -ge 15 ]; then
            err "컨테이너 시작 시간 초과 (30초)"
            exit 1
        fi
    done

    log "  컨테이너 생성 및 시작 완료"
}

# ─── [3/6] 의존성 설치 ────────────────────────────────────────────────
install_dependencies() {
    log "[3/6] 의존성 설치 중..."

    pct exec "$CTID" -- bash -c "
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -qq
        apt-get install -y -qq curl ca-certificates gnupg lsb-release >/dev/null 2>&1
    "
    info "  기본 패키지 설치 완료"

    # Docker 설치
    pct exec "$CTID" -- bash -c "
        export DEBIAN_FRONTEND=noninteractive
        install -m 0755 -d /etc/apt/keyrings
        curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
        chmod a+r /etc/apt/keyrings/docker.gpg
        echo \"deb [arch=\$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian \$(lsb_release -cs) stable\" > /etc/apt/sources.list.d/docker.list
        apt-get update -qq
        apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null 2>&1
        systemctl enable docker
        systemctl start docker
    "
    info "  Docker 설치 완료"
}

# ─── [4/6] 로케일 설정 ────────────────────────────────────────────────
setup_locale() {
    log "[4/6] 로케일 설정 중..."

    pct exec "$CTID" -- bash -c "
        export DEBIAN_FRONTEND=noninteractive
        apt-get install -y -qq locales >/dev/null 2>&1
        sed -i 's/# en_US.UTF-8 UTF-8/en_US.UTF-8 UTF-8/' /etc/locale.gen
        locale-gen >/dev/null 2>&1
        update-locale LANG=en_US.UTF-8
    "

    log "  로케일 설정 완료 (en_US.UTF-8)"
}

# ─── [5/6] HeyForm 배포 ──────────────────────────────────────────────
deploy_heyform() {
    log "[5/6] HeyForm 배포 중..."

    # 시크릿 키 생성
    local session_key
    local encryption_key
    session_key=$(openssl rand -hex 32)
    encryption_key=$(openssl rand -hex 32)

    info "  SESSION_KEY, FORM_ENCRYPTION_KEY 자동 생성됨"

    # 작업 디렉토리 생성
    pct exec "$CTID" -- mkdir -p /opt/heyform

    # docker-compose.yml 생성
    cat <<COMPOSE_EOF | pct exec "$CTID" -- tee /opt/heyform/docker-compose.yml >/dev/null
services:
  heyform:
    image: heyform/community-edition:latest
    container_name: heyform
    restart: unless-stopped
    depends_on:
      mongo:
        condition: service_started
      keydb:
        condition: service_started
    environment:
      APP_HOMEPAGE_URL: "${APP_HOMEPAGE_URL}"
      SESSION_KEY: "${session_key}"
      FORM_ENCRYPTION_KEY: "${encryption_key}"
      MONGO_URI: "mongodb://mongo:27017/heyform"
      REDIS_HOST: keydb
      REDIS_PORT: "6379"
    volumes:
      - ./assets:/app/static/upload
    ports:
      - "${HEYFORM_PORT}:8000"

  mongo:
    image: percona/percona-server-mongodb:4.4
    container_name: heyform-mongo
    restart: unless-stopped
    volumes:
      - ./database:/data/db

  keydb:
    image: eqalpha/keydb:latest
    container_name: heyform-keydb
    restart: unless-stopped
    volumes:
      - ./keydb:/data
COMPOSE_EOF

    info "  docker-compose.yml 생성 완료"

    # Docker Compose 실행
    pct exec "$CTID" -- bash -c "cd /opt/heyform && docker compose pull -q 2>/dev/null && docker compose up -d"

    log "  HeyForm 컨테이너 시작됨"
}

# ─── [6/6] 헬스체크 ───────────────────────────────────────────────────
health_check() {
    log "[6/6] 헬스체크 진행 중..."

    local max_attempts=30
    local attempt=0

    while [ $attempt -lt $max_attempts ]; do
        attempt=$((attempt + 1))
        if pct exec "$CTID" -- curl -sf -o /dev/null "http://localhost:${HEYFORM_PORT}" 2>/dev/null; then
            log "  헬스체크 통과 (${attempt}/${max_attempts})"
            return 0
        fi
        info "  대기 중... (${attempt}/${max_attempts})"
        sleep 5
    done

    err "헬스체크 실패 — ${max_attempts}회 시도 후에도 응답 없음"
    err "수동 확인: pct exec ${CTID} -- docker compose -f /opt/heyform/docker-compose.yml logs"
    exit 1
}

# ─── 설치 완료 요약 ───────────────────────────────────────────────────
print_summary() {
    echo ""
    echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN} HeyForm 설치 완료${NC}"
    echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}"
    echo ""
    echo -e "  CTID:       ${BLUE}${CTID}${NC}"
    echo -e "  Hostname:   ${BLUE}${CT_HOSTNAME}${NC}"
    echo -e "  IP:         ${BLUE}${CT_IP}${NC}"
    echo -e "  접속 URL:   ${BLUE}${APP_HOMEPAGE_URL}${NC}"
    echo -e "  포트:       ${BLUE}${HEYFORM_PORT}${NC}"
    echo ""
    echo -e "  관리 명령어:"
    echo -e "    로그:     ${YELLOW}pct exec ${CTID} -- docker compose -f /opt/heyform/docker-compose.yml logs -f${NC}"
    echo -e "    재시작:   ${YELLOW}pct exec ${CTID} -- docker compose -f /opt/heyform/docker-compose.yml restart${NC}"
    echo -e "    중지:     ${YELLOW}pct exec ${CTID} -- docker compose -f /opt/heyform/docker-compose.yml down${NC}"
    echo ""
    echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}"
}

# ─── main ─────────────────────────────────────────────────────────────
main() {
    echo ""
    echo -e "${GREEN}HeyForm LXC 설치 스크립트${NC}"
    echo -e "${GREEN}https://github.com/heyform/heyform${NC}"
    echo ""

    # Proxmox 호스트 확인
    if ! command -v pct &>/dev/null; then
        err "이 스크립트는 Proxmox VE 호스트에서 실행해야 합니다."
        exit 1
    fi

    validate_inputs
    download_template
    create_container
    install_dependencies
    setup_locale
    deploy_heyform
    health_check
    print_summary
}

main "$@"
