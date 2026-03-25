#!/usr/bin/env bats
# Docker-dependent smoke tests
# 전용 테스트 dal을 wake/sleep하므로 운영 dal에 영향 없음.
#
# 실행:
#   DALCENTER_URL=http://localhost:11191 \
#   DALCENTER_LOCALDAL_PATH=/root/bridge-of-gaya-script/.dal \
#   bats tests/smoke-docker.bats
#
# 전제: dalcenter serve 실행 중, Docker 사용 가능, claude 이미지 존재

DALCENTER="${DALCENTER:-dalcenter}"
# 테스트용 dal — 반드시 localdal에 존재해야 함
TEST_DAL="story-checker"

setup_file() {
    # 테스트 시작 전 상태 저장
    export ORIGINAL_STATUS=$($DALCENTER ps 2>/dev/null | grep "$TEST_DAL" | awk '{print $4}')
}

teardown_file() {
    # 테스트 후 원래 상태로 복구
    if [ "$ORIGINAL_STATUS" = "running" ]; then
        $DALCENTER wake "$TEST_DAL" 2>/dev/null || true
    else
        $DALCENTER sleep "$TEST_DAL" 2>/dev/null || true
    fi
}

# ── wake ──

@test "wake: 컨테이너 생성" {
    # 먼저 sleep 상태로 만듦
    $DALCENTER sleep "$TEST_DAL" 2>/dev/null || true
    sleep 2

    run $DALCENTER wake "$TEST_DAL"
    [ "$status" -eq 0 ]
    [[ "$output" == *"wake"* ]]
}

@test "wake 후: Docker 컨테이너 존재" {
    run docker ps --filter "name=dal-$TEST_DAL" --format "{{.Names}}"
    [ "$status" -eq 0 ]
    [[ "$output" == "dal-$TEST_DAL" ]]
}

@test "wake 후: 컨테이너 running" {
    run docker inspect "dal-$TEST_DAL" --format "{{.State.Status}}"
    [ "$status" -eq 0 ]
    [ "$output" = "running" ]
}

# ── injectCli ──

@test "injectCli: dalcli 바이너리 주입됨" {
    run docker exec "dal-$TEST_DAL" which dalcli
    [ "$status" -eq 0 ]
    [[ "$output" == *"/usr/local/bin/dalcli"* ]]
}

@test "injectCli: dalcli 실행 가능" {
    run docker exec "dal-$TEST_DAL" dalcli --help
    [ "$status" -eq 0 ]
    [[ "$output" == *"run"* ]]
}

# ── 컨테이너 내부 마운트 ──

@test "workspace 마운트" {
    run docker exec "dal-$TEST_DAL" ls /workspace/SYNOPSIS.md
    [ "$status" -eq 0 ]
}

@test "instructions → CLAUDE.md 변환" {
    run docker exec "dal-$TEST_DAL" cat /root/.claude/CLAUDE.md
    [ "$status" -eq 0 ]
    [ -n "$output" ]
}

@test "skills 마운트" {
    run docker exec "dal-$TEST_DAL" ls /root/.claude/skills/
    [ "$status" -eq 0 ]
    [ -n "$output" ]
}

@test "dal 디렉토리 마운트 (read-only)" {
    run docker exec "dal-$TEST_DAL" ls /dal/dal.cue
    [ "$status" -eq 0 ]
}

# ── 환경변수 ──

@test "DAL_NAME 설정됨" {
    result=$(docker exec "dal-$TEST_DAL" printenv DAL_NAME)
    [ "$result" = "$TEST_DAL" ]
}

@test "DAL_ROLE 설정됨" {
    result=$(docker exec "dal-$TEST_DAL" printenv DAL_ROLE)
    [ -n "$result" ]
}

@test "DALCENTER_URL 설정됨" {
    result=$(docker exec "dal-$TEST_DAL" printenv DALCENTER_URL)
    [[ "$result" == *"host.docker.internal"* ]]
}

@test "GITHUB_TOKEN 설정됨" {
    result=$(docker exec "dal-$TEST_DAL" printenv GITHUB_TOKEN)
    [ -n "$result" ]
}

@test "GH_TOKEN 설정됨" {
    result=$(docker exec "dal-$TEST_DAL" printenv GH_TOKEN)
    [ -n "$result" ]
}

# ── host resolution ──

@test "host.docker.internal 해석" {
    run docker exec "dal-$TEST_DAL" getent hosts host.docker.internal
    [ "$status" -eq 0 ]
    [[ "$output" == *"172.17"* ]]
}

# ── dalcli 통신 ──

@test "dalcli ps: 데몬 통신" {
    run docker exec "dal-$TEST_DAL" dalcli ps
    [ "$status" -eq 0 ]
    [[ "$output" == *"$TEST_DAL"* ]]
}

@test "dalcli status: 자기 상태" {
    run docker exec "dal-$TEST_DAL" dalcli status
    [ "$status" -eq 0 ]
    [[ "$output" == *"$TEST_DAL"* ]]
    [[ "$output" == *"running"* ]]
}

# ── agent-config API ──

@test "agent-config: bot_token 존재" {
    port="${DALCENTER_URL##*:}"
    result=$(docker exec "dal-$TEST_DAL" curl -sf "http://host.docker.internal:$port/api/agent-config/$TEST_DAL")
    [[ "$result" == *"bot_token"* ]]
    # bot_token이 비어있지 않은지
    token=$(echo "$result" | python3 -c "import json,sys; print(json.load(sys.stdin).get('bot_token',''))" 2>/dev/null)
    [ -n "$token" ]
}

# ── dalcli run (entrypoint) ──

@test "entrypoint: dalcli run 자동 시작" {
    # 잠깐 대기 (entrypoint가 dalcli를 기다림)
    sleep 5
    pid=$(docker exec "dal-$TEST_DAL" pgrep -f "dalcli run" 2>/dev/null)
    [ -n "$pid" ]
}

# ── logs ──

@test "dalcenter logs: 로그 조회" {
    run $DALCENTER logs "$TEST_DAL"
    [ "$status" -eq 0 ]
}

@test "docker logs: 컨테이너 로그" {
    run docker logs "dal-$TEST_DAL" --tail 5
    [ "$status" -eq 0 ]
    [[ "$output" == *"entrypoint"* ]] || [[ "$output" == *"agent"* ]]
}

# ── sync ──

@test "sync: 구조 변경 감지" {
    run $DALCENTER sync
    [ "$status" -eq 0 ]
}

# ── git inside container ──

@test "git: workspace에서 사용 가능" {
    run docker exec "dal-$TEST_DAL" git -C /workspace status --short
    [ "$status" -eq 0 ]
}

@test "git: 사용자 설정됨" {
    name=$(docker exec "dal-$TEST_DAL" printenv GIT_AUTHOR_NAME)
    [ -n "$name" ]
}

# ── claude ──

@test "claude: 바이너리 존재" {
    run docker exec "dal-$TEST_DAL" which claude
    [ "$status" -eq 0 ]
}

# ── sleep ──

@test "sleep: 컨테이너 제거" {
    run $DALCENTER sleep "$TEST_DAL"
    [ "$status" -eq 0 ]
    [[ "$output" == *"sleep"* ]]
}

@test "sleep 후: Docker 컨테이너 없음" {
    run docker ps --filter "name=dal-$TEST_DAL" --format "{{.Names}}"
    [ -z "$output" ]
}

@test "sleep 후: ps에서 제거" {
    run $DALCENTER ps
    [[ "$output" != *"$TEST_DAL"*"running"* ]]
}

# ── 재wake (stale container 정리) ──

@test "재wake: stale 컨테이너 정리 후 정상 시작" {
    run $DALCENTER wake "$TEST_DAL"
    [ "$status" -eq 0 ]

    # 다시 sleep → wake (stale 정리 테스트)
    $DALCENTER sleep "$TEST_DAL"
    sleep 1

    run $DALCENTER wake "$TEST_DAL"
    [ "$status" -eq 0 ]

    # 정리
    $DALCENTER sleep "$TEST_DAL" 2>/dev/null || true
}
