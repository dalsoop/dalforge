#!/usr/bin/env bats
# dalcenter smoke tests
# 실행: bats tests/smoke.bats

DALCENTER="${DALCENTER:-dalcenter}"

setup_file() {
    export DALCENTER_LOCALDAL_PATH="$BATS_FILE_TMPDIR/.dal"
}

# --- CLI 기본 ---

@test "help 출력" {
    run $DALCENTER --help
    [ "$status" -eq 0 ]
    [[ "$output" == *"wake"* ]]
    [[ "$output" == *"sleep"* ]]
    [[ "$output" == *"sync"* ]]
}

@test "모든 서브커맨드 존재" {
    for cmd in serve init validate wake sleep sync status ps logs attach; do
        run $DALCENTER $cmd --help
        [ "$status" -eq 0 ]
    done
}

# --- INIT ---

@test "init 동작" {
    run $DALCENTER init
    [ "$status" -eq 0 ]
    [ -d "$DALCENTER_LOCALDAL_PATH" ]
    [ -f "$DALCENTER_LOCALDAL_PATH/dal.spec.cue" ]
}

# --- VALIDATE ---

@test "validate 빈 localdal 에러" {
    run $DALCENTER validate
    [ "$status" -ne 0 ]
    [[ "$output" == *"no dals found"* ]]
}

@test "validate leader 없으면 에러" {
    mkdir -p "$DALCENTER_LOCALDAL_PATH/dev"
    cat > "$DALCENTER_LOCALDAL_PATH/dev/dal.cue" << 'EOF'
uuid:    "test-001"
name:    "dev"
version: "1.0.0"
player:  "claude"
role:    "member"
skills:  []
hooks:   []
EOF
    echo "# dev" > "$DALCENTER_LOCALDAL_PATH/dev/instructions.md"

    run $DALCENTER validate
    [ "$status" -ne 0 ]
    [[ "$output" == *"no leader"* ]]
}

@test "validate 정상 통과" {
    mkdir -p "$DALCENTER_LOCALDAL_PATH/leader"
    cat > "$DALCENTER_LOCALDAL_PATH/leader/dal.cue" << 'EOF'
uuid:    "leader-001"
name:    "leader"
version: "1.0.0"
player:  "claude"
role:    "leader"
skills:  []
hooks:   []
EOF
    echo "# leader" > "$DALCENTER_LOCALDAL_PATH/leader/instructions.md"

    run $DALCENTER validate
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok"* ]]
}

# --- STATUS ---

@test "status 목록 표시" {
    run $DALCENTER status
    [ "$status" -eq 0 ]
    [[ "$output" == *"leader"* ]]
    [[ "$output" == *"dev"* ]]
}

@test "status 개별 dal 표시" {
    run $DALCENTER status dev
    [ "$status" -eq 0 ]
    [[ "$output" == *"uuid"* ]]
    [[ "$output" == *"player"* ]]
}

@test "status 존재하지 않는 dal 에러" {
    run $DALCENTER status nonexistent
    [ "$status" -ne 0 ]
}

# --- WAKE/SLEEP (데몬 없이 에러 확인) ---

@test "wake 데몬 없으면 에러" {
    export DALCENTER_URL="http://localhost:19999"
    run $DALCENTER wake dev
    [ "$status" -ne 0 ]
    [[ "$output" == *"daemon unreachable"* ]]
}

@test "sleep 데몬 없으면 에러" {
    export DALCENTER_URL="http://localhost:19999"
    run $DALCENTER sleep dev
    [ "$status" -ne 0 ]
    [[ "$output" == *"daemon unreachable"* ]]
}

@test "ps 데몬 없으면 에러" {
    export DALCENTER_URL="http://localhost:19999"
    run $DALCENTER ps
    [ "$status" -ne 0 ]
    [[ "$output" == *"daemon unreachable"* ]]
}

@test "sync 데몬 없으면 에러" {
    export DALCENTER_URL="http://localhost:19999"
    run $DALCENTER sync
    [ "$status" -ne 0 ]
    [[ "$output" == *"daemon unreachable"* ]]
}
