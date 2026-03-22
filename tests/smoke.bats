#!/usr/bin/env bats
# dalcenter smoke tests
# 실행: bats tests/smoke.bats
# LXC 안에서: dalcenter 바이너리가 PATH에 있어야 함

DALCENTER="${DALCENTER:-dalcenter}"

# --- CLI 기본 ---

@test "help 출력" {
    run $DALCENTER --help
    [ "$status" -eq 0 ]
    [[ "$output" == *"DalForge"* ]]
}

@test "모든 서브커맨드 존재" {
    for cmd in catalog join list status validate export unexport start stop restart reconcile watch provision destroy secret; do
        run $DALCENTER $cmd --help
        [ "$status" -eq 0 ]
    done
}

# --- CATALOG ---

@test "catalog search 동작" {
    run $DALCENTER catalog search ""
    [ "$status" -eq 0 ]
    [[ "$output" == *"NAME"* ]]
}

@test "catalog search 특정 패키지" {
    run $DALCENTER catalog search agent-coach
    [ "$status" -eq 0 ]
    [[ "$output" == *"agent-coach"* ]]
}

# --- JOIN / LIST / STATUS ---

@test "join 클라우드 패키지" {
    run $DALCENTER join agent-coach
    [ "$status" -eq 0 ]
    [[ "$output" == *"instance created"* ]]
}

@test "list 등록된 인스턴스 표시" {
    run $DALCENTER list
    [ "$status" -eq 0 ]
    [[ "$output" == *"agent-coach"* ]]
}

@test "status 인스턴스 상세" {
    run $DALCENTER status agent-coach
    [ "$status" -eq 0 ]
    [[ "$output" == *"dal_id"* ]]
    [[ "$output" == *"source_ref"* ]]
}

# --- VALIDATE ---

@test "validate 이름으로 동작" {
    run $DALCENTER validate agent-coach
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok"* ]]
}

# --- EXPORT / UNEXPORT ---

@test "export 이름으로 동작" {
    run $DALCENTER export agent-coach
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok"* ]]
}

@test "unexport 이름으로 동작" {
    run $DALCENTER unexport agent-coach
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok"* ]]
}

# --- START / STOP / RESTART ---

@test "start 프로세스 실행" {
    run $DALCENTER start agent-coach
    [ "$status" -eq 0 ]
    [[ "$output" == *"started"* ]]
}

@test "stop 프로세스 정지" {
    run $DALCENTER stop agent-coach
    [ "$status" -eq 0 ]
    [[ "$output" == *"stopped"* ]]
}

@test "restart 프로세스 재시작" {
    run $DALCENTER restart agent-coach
    [ "$status" -eq 0 ]
    [[ "$output" == *"restarted"* ]]
}

@test "stop 정리" {
    run $DALCENTER stop agent-coach
    [ "$status" -eq 0 ]
}

# --- RECONCILE ---

@test "reconcile 동작" {
    run $DALCENTER reconcile
    [ "$status" -eq 0 ]
}

# --- PROVISION (dry-run) ---

@test "provision dry-run 동작" {
    run $DALCENTER provision agent-coach --dry-run --vmid 999 --storage local-lvm --bridge vmbr0
    [ "$status" -eq 0 ]
    [[ "$output" == *"pct create 999"* ]]
    [[ "$output" == *"pct start"* ]]
}

@test "provision dry-run credential sync 표시" {
    run $DALCENTER provision agent-coach --dry-run --vmid 999 --storage local-lvm --bridge vmbr0
    [ "$status" -eq 0 ]
    [[ "$output" == *"proxmox-host-setup ai mount"* ]]
}

# --- SECRET ---

@test "secret list 동작 (빈 상태)" {
    run $DALCENTER secret list
    [ "$status" -eq 0 ]
}

# --- DESTROY (no-op) ---

@test "destroy 컨테이너 없으면 no-op" {
    run $DALCENTER destroy agent-coach
    [ "$status" -eq 0 ]
    [[ "$output" == *"nothing to destroy"* ]]
}

# --- 에러 케이스 ---

@test "status 존재하지 않는 인스턴스 에러" {
    run $DALCENTER status nonexistent-instance-xyz
    [ "$status" -ne 0 ]
}

@test "validate 존재하지 않는 경로 에러" {
    run $DALCENTER validate /nonexistent/path
    [ "$status" -ne 0 ]
}
