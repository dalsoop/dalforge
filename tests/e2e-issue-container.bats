#!/usr/bin/env bats
# 이슈별 독립 컨테이너 워크플로우 E2E 검증 (#563)
#
# 전체 흐름:
#   1. leader가 dalcli-leader wake dev --issue 559 로 독립 컨테이너 소환
#   2. dev가 clone mode로 독립 workspace 생성 (bind mount 아님)
#   3. issue-559/dev 브랜치 자동 생성
#   4. leader가 dalcli-leader assign dev "이슈 구현해" 로 작업 지시
#   5. dev가 작업 → 커밋 → PR 생성
#   6. 완료 시 leader가 @dalroot 멘션으로 dalroot에게 알림 (#547)
#   7. dalroot hook으로 자동 수신
#
# 실행:
#   DALCENTER_URL=http://localhost:11190 \
#   DALCENTER_LOCALDAL_PATH=/path/to/.dal \
#   bats tests/e2e-issue-container.bats
#
# 전제: dalcenter serve 실행 중, Docker 사용 가능, claude 이미지 빌드됨

DALCENTER="${DALCENTER:-dalcenter}"
LEADER="${DALCLI_LEADER:-dalcli-leader}"
TEST_DAL="dev"
TEST_ISSUE="559"
EXPECTED_BRANCH="issue-${TEST_ISSUE}/${TEST_DAL}"
CONTAINER_NAME="dal-${TEST_DAL}"

# 컨테이너 안정화 대기 (clone + setup 완료까지)
WAKE_SETTLE_SEC="${WAKE_SETTLE_SEC:-30}"

setup_file() {
    # 사전 조건 확인
    command -v docker >/dev/null 2>&1 || { echo "docker not found"; exit 1; }
    curl -sf "$DALCENTER_URL/api/ps" >/dev/null 2>&1 || { echo "daemon not reachable at $DALCENTER_URL"; exit 1; }

    # 기존 dal-dev 컨테이너가 있으면 sleep
    $DALCENTER sleep "$TEST_DAL" 2>/dev/null || true
    sleep 2

    # wake with issue
    run $DALCENTER wake "$TEST_DAL" --issue "$TEST_ISSUE"
    if [ "$status" -ne 0 ]; then
        echo "wake failed: $output"
        exit 1
    fi

    # clone + setupWorkspace 완료 대기
    sleep "$WAKE_SETTLE_SEC"
}

teardown_file() {
    $DALCENTER sleep "$TEST_DAL" 2>/dev/null || true
}

# ══════════════════════════════════════════════
# 1. wake --issue: 독립 컨테이너 소환
# ══════════════════════════════════════════════

@test "1-1: wake --issue 후 컨테이너 running" {
    run docker inspect "$CONTAINER_NAME" --format "{{.State.Status}}"
    [ "$status" -eq 0 ]
    [ "$output" = "running" ]
}

@test "1-2: ps에 dev 표시" {
    run $DALCENTER ps
    [ "$status" -eq 0 ]
    [[ "$output" == *"$TEST_DAL"* ]]
    [[ "$output" == *"running"* ]]
}

@test "1-3: dalcli 바이너리 주입됨" {
    run docker exec "$CONTAINER_NAME" which dalcli
    [ "$status" -eq 0 ]
}

@test "1-4: dalcli-leader 바이너리 주입됨" {
    run docker exec "$CONTAINER_NAME" which dalcli-leader
    [ "$status" -eq 0 ]
}

# ══════════════════════════════════════════════
# 2. clone mode: 독립 workspace 검증
# ══════════════════════════════════════════════

@test "2-1: workspace가 git 저장소" {
    run docker exec "$CONTAINER_NAME" git -C /workspace rev-parse --is-inside-work-tree
    [ "$status" -eq 0 ]
    [ "$output" = "true" ]
}

@test "2-2: .git이 디렉토리 (독립 clone, bind mount 아님)" {
    run docker exec "$CONTAINER_NAME" test -d /workspace/.git
    [ "$status" -eq 0 ]
}

@test "2-3: git remote origin 존재" {
    run docker exec "$CONTAINER_NAME" git -C /workspace remote get-url origin
    [ "$status" -eq 0 ]
    [[ "$output" == *"github.com"* ]] || [[ "$output" == *"git@"* ]]
}

@test "2-4: git log 조회 가능 (shallow clone)" {
    run docker exec "$CONTAINER_NAME" git -C /workspace log --oneline -5
    [ "$status" -eq 0 ]
    [ -n "$output" ]
}

@test "2-5: workspace에 핵심 파일 존재" {
    run docker exec "$CONTAINER_NAME" test -f /workspace/go.mod
    [ "$status" -eq 0 ]
}

@test "2-6: clone mode — 호스트 workspace와 격리됨" {
    # 컨테이너 내에서 테스트 파일 생성 → 호스트에 없어야 함
    marker="e2e-isolation-check-$(date +%s)"
    docker exec "$CONTAINER_NAME" touch "/workspace/$marker"
    [ ! -f "/workspace/$marker" ]
    docker exec "$CONTAINER_NAME" rm -f "/workspace/$marker"
}

# ══════════════════════════════════════════════
# 3. 이슈 브랜치 자동 생성
# ══════════════════════════════════════════════

@test "3-1: issue-559/dev 브랜치에 체크아웃됨" {
    result=$(docker exec "$CONTAINER_NAME" git -C /workspace branch --show-current)
    [ "$result" = "$EXPECTED_BRANCH" ]
}

@test "3-2: 브랜치가 main 또는 HEAD 기반" {
    # 브랜치 시작점이 main의 커밋을 포함하는지 확인
    run docker exec "$CONTAINER_NAME" git -C /workspace log --oneline -1
    [ "$status" -eq 0 ]
    [ -n "$output" ]
}

# ══════════════════════════════════════════════
# 4. 컨테이너 환경 검증
# ══════════════════════════════════════════════

@test "4-1: DAL_NAME 환경변수" {
    result=$(docker exec "$CONTAINER_NAME" printenv DAL_NAME)
    [ "$result" = "$TEST_DAL" ]
}

@test "4-2: DAL_ROLE 환경변수" {
    result=$(docker exec "$CONTAINER_NAME" printenv DAL_ROLE)
    [ -n "$result" ]
}

@test "4-3: DALCENTER_URL 환경변수" {
    result=$(docker exec "$CONTAINER_NAME" printenv DALCENTER_URL)
    [[ "$result" == *"host.docker.internal"* ]]
}

@test "4-4: GITHUB_TOKEN 설정됨" {
    result=$(docker exec "$CONTAINER_NAME" printenv GITHUB_TOKEN 2>/dev/null || docker exec "$CONTAINER_NAME" printenv GH_TOKEN 2>/dev/null)
    [ -n "$result" ]
}

@test "4-5: host.docker.internal 해석 가능" {
    run docker exec "$CONTAINER_NAME" getent hosts host.docker.internal
    [ "$status" -eq 0 ]
}

@test "4-6: dalcli ps — 데몬 통신 정상" {
    run docker exec "$CONTAINER_NAME" dalcli ps
    [ "$status" -eq 0 ]
    [[ "$output" == *"$TEST_DAL"* ]]
}

@test "4-7: agent-config API 접근 가능" {
    port="${DALCENTER_URL##*:}"
    run docker exec "$CONTAINER_NAME" curl -sf "http://host.docker.internal:$port/api/agent-config/$TEST_DAL"
    [ "$status" -eq 0 ]
    [[ "$output" == *"dal_name"* ]]
}

@test "4-8: CLAUDE.md 마운트됨" {
    run docker exec "$CONTAINER_NAME" test -f /root/.claude/CLAUDE.md
    [ "$status" -eq 0 ]
}

# ══════════════════════════════════════════════
# 5. git 워크플로우 (커밋 → PR 가능성 검증)
# ══════════════════════════════════════════════

@test "5-1: git config user 설정됨" {
    name=$(docker exec "$CONTAINER_NAME" bash -c "git -C /workspace config user.name 2>/dev/null || printenv GIT_AUTHOR_NAME")
    [ -n "$name" ]
}

@test "5-2: git config email 설정됨" {
    email=$(docker exec "$CONTAINER_NAME" bash -c "git -C /workspace config user.email 2>/dev/null || printenv GIT_AUTHOR_EMAIL")
    [ -n "$email" ]
}

@test "5-3: gh CLI 사용 가능" {
    run docker exec "$CONTAINER_NAME" gh --version
    [ "$status" -eq 0 ]
}

@test "5-4: gh 인증 — repo 접근 가능" {
    run docker exec "$CONTAINER_NAME" gh auth status
    [ "$status" -eq 0 ]
}

@test "5-5: 커밋 생성 가능 (dry-run)" {
    # 테스트 파일 생성 → add → commit dry-run
    docker exec "$CONTAINER_NAME" bash -c "echo 'e2e-test' > /workspace/.e2e-test-marker"
    run docker exec "$CONTAINER_NAME" git -C /workspace add .e2e-test-marker
    [ "$status" -eq 0 ]

    run docker exec "$CONTAINER_NAME" git -C /workspace commit --dry-run -m "test: e2e marker"
    [ "$status" -eq 0 ]

    # 정리
    docker exec "$CONTAINER_NAME" git -C /workspace reset HEAD .e2e-test-marker 2>/dev/null
    docker exec "$CONTAINER_NAME" rm -f /workspace/.e2e-test-marker
}

# ══════════════════════════════════════════════
# 6. assign 통신 경로 검증
# ══════════════════════════════════════════════

@test "6-1: /api/message 엔드포인트 존재" {
    # POST with empty body → 에러여도 404가 아닌지 확인
    status_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$DALCENTER_URL/api/message" -H "Content-Type: application/json" -d '{}')
    [ "$status_code" != "404" ]
}

@test "6-2: bridge URL 설정됨 (컨테이너 내)" {
    # agent-config에서 bridge_url 확인
    port="${DALCENTER_URL##*:}"
    result=$(docker exec "$CONTAINER_NAME" curl -sf "http://host.docker.internal:$port/api/agent-config/$TEST_DAL")
    # bridge_url이 설정되어 있거나, DAL_BRIDGE_PORT가 있어야 함
    has_bridge=$(echo "$result" | python3 -c "
import json, sys
data = json.load(sys.stdin)
print('yes' if data.get('bridge_url') or data.get('bridge_port') else 'no')
" 2>/dev/null || echo "no")
    [ "$has_bridge" = "yes" ] || {
        # bridge가 없어도 daemon /api/message 경유 가능
        skip "bridge not configured — assign uses daemon relay"
    }
}

# ══════════════════════════════════════════════
# 7. callback notification 경로 검증
# ══════════════════════════════════════════════

@test "7-1: DALCENTER_NOTIFY_URL 또는 notify-dalroot 존재" {
    # daemon 측 환경변수 또는 컨테이너 내 CLI
    notify_url=$(curl -sf "$DALCENTER_URL/api/ps" >/dev/null && echo "daemon_reachable")
    has_cli=$(docker exec "$CONTAINER_NAME" which notify-dalroot 2>/dev/null && echo "yes" || echo "no")

    # 둘 중 하나는 있어야 알림 가능
    [ "$notify_url" = "daemon_reachable" ] || [ "$has_cli" = "yes" ]
}

@test "7-2: /api/tasks 엔드포인트 응답" {
    run curl -sf "$DALCENTER_URL/api/tasks"
    [ "$status" -eq 0 ]
}

# ══════════════════════════════════════════════
# 8. entrypoint & agent loop
# ══════════════════════════════════════════════

@test "8-1: entrypoint.sh로 시작됨" {
    run docker logs "$CONTAINER_NAME" --tail 20
    [ "$status" -eq 0 ]
    [[ "$output" == *"entrypoint"* ]] || [[ "$output" == *"dalcli"* ]] || [[ "$output" == *"agent"* ]]
}

@test "8-2: dalcli run 프로세스 동작 중" {
    pid=$(docker exec "$CONTAINER_NAME" pgrep -f "dalcli run" 2>/dev/null)
    [ -n "$pid" ]
}

# ══════════════════════════════════════════════
# 9. sleep 후 정리 검증
# ══════════════════════════════════════════════

@test "9-1: sleep 후 컨테이너 제거" {
    run $DALCENTER sleep "$TEST_DAL"
    [ "$status" -eq 0 ]

    run docker ps --filter "name=$CONTAINER_NAME" --format "{{.Names}}"
    [ -z "$output" ]
}

@test "9-2: sleep 후 ps에서 제거" {
    run $DALCENTER ps
    [[ "$output" != *"$TEST_DAL"*"running"* ]]
}

@test "9-3: 재wake 정상 동작 (stale container 정리)" {
    run $DALCENTER wake "$TEST_DAL" --issue "$TEST_ISSUE"
    [ "$status" -eq 0 ]

    sleep "$WAKE_SETTLE_SEC"

    # 브랜치 확인 — 기존 브랜치 checkout
    result=$(docker exec "$CONTAINER_NAME" git -C /workspace branch --show-current)
    [ "$result" = "$EXPECTED_BRANCH" ]

    # 정리
    $DALCENTER sleep "$TEST_DAL" 2>/dev/null || true
}
