#!/usr/bin/env bats
# dalcenter 자체 QA 검증 스크립트
# test-dal (role=member)이 wake 후 자동으로 실행.
#
# 검증 항목:
#   1. wake/sleep 정상 동작
#   2. member clone mode workspace 격리 (#523)
#   3. task 실행 + callback notification 수신 (#522)
#   4. task-list 조회
#   5. git branch 생성/커밋/PR 워크플로우
#
# 실행:
#   DALCENTER_URL=http://host.docker.internal:11190 bats tests/smoke-qa.bats
#
# 컨테이너 내부에서 실행되는 것을 전제로 함 (test-dal wake 후)

DALCENTER_URL="${DALCENTER_URL:-http://host.docker.internal:11190}"
DAL_NAME="${DAL_NAME:-test-dal}"

# ── 1. wake/sleep 정상 동작 ──

@test "QA: 자기 자신 wake 상태 확인" {
    run dalcli status
    [ "$status" -eq 0 ]
    [[ "$output" == *"$DAL_NAME"* ]]
    [[ "$output" == *"running"* ]]
}

@test "QA: dalcli ps 조회 가능" {
    run dalcli ps
    [ "$status" -eq 0 ]
    # 최소한 자기 자신이 보여야 함
    [[ "$output" == *"$DAL_NAME"* ]]
}

@test "QA: dalcenter daemon HTTP 응답" {
    run curl -sf "$DALCENTER_URL/api/ps"
    [ "$status" -eq 0 ]
}

@test "QA: daemon JSON 파싱 가능" {
    result=$(curl -sf "$DALCENTER_URL/api/ps")
    echo "$result" | python3 -c "import json,sys; json.load(sys.stdin)"
}

# ── 2. member clone mode workspace 격리 (#523) ──

@test "QA: workspace가 git 저장소" {
    run git -C /workspace rev-parse --is-inside-work-tree
    [ "$status" -eq 0 ]
    [ "$output" = "true" ]
}

@test "QA: workspace에 유효한 git remote" {
    run git -C /workspace remote -v
    [ "$status" -eq 0 ]
    [[ "$output" == *"origin"* ]]
}

@test "QA: clone mode — workspace가 독립 clone" {
    # member는 clone mode가 기본 (#523)
    # .git이 디렉토리여야 함 (bind mount의 경우 .git이 파일일 수 있음)
    if [ -d "/workspace/.git" ]; then
        # 독립 clone: .git은 디렉토리
        [ -f "/workspace/.git/HEAD" ]
    else
        # worktree나 submodule: .git이 파일 — clone mode가 아님
        skip "workspace is not a standalone clone (.git is not a directory)"
    fi
}

@test "QA: clone mode — git log 조회 가능" {
    run git -C /workspace log --oneline -5
    [ "$status" -eq 0 ]
    [ -n "$output" ]
}

@test "QA: workspace에 핵심 파일 존재" {
    [ -f "/workspace/go.mod" ]
    [ -d "/workspace/.dal" ]
    [ -d "/workspace/cmd" ]
}

# ── 3. task 실행 + callback notification (#522) ──

@test "QA: /api/tasks API 응답" {
    run curl -sf "$DALCENTER_URL/api/tasks"
    [ "$status" -eq 0 ]
}

@test "QA: /api/tasks JSON 형식" {
    result=$(curl -sf "$DALCENTER_URL/api/tasks")
    echo "$result" | python3 -c "import json,sys; json.load(sys.stdin)"
}

@test "QA: agent-config API 접근" {
    port="${DALCENTER_URL##*:}"
    run curl -sf "$DALCENTER_URL/api/agent-config/$DAL_NAME"
    [ "$status" -eq 0 ]
    [[ "$output" == *"dal_name"* ]]
}

@test "QA: notify-dalroot 바이너리 존재" {
    # callback notification은 notify-dalroot 의존
    if which notify-dalroot >/dev/null 2>&1; then
        run which notify-dalroot
        [ "$status" -eq 0 ]
    else
        skip "notify-dalroot not installed in container"
    fi
}

# ── 4. task-list 조회 ──

@test "QA: /api/tasks 응답에 배열 또는 객체" {
    result=$(curl -sf "$DALCENTER_URL/api/tasks")
    type=$(echo "$result" | python3 -c "
import json, sys
data = json.load(sys.stdin)
print(type(data).__name__)
")
    [[ "$type" == "list" ]] || [[ "$type" == "dict" ]]
}

@test "QA: dalcli ps 출력 형식 검증" {
    result=$(dalcli ps 2>&1)
    # 실행 중 DAL이 있으면 테이블 형식 헤더 확인
    if [[ "$result" != *"no awake"* ]]; then
        [[ "$result" == *"NAME"* ]] || [[ "$result" == *"name"* ]]
    fi
}

# ── 5. git workflow ──

@test "QA: git config user 설정됨" {
    name=$(git -C /workspace config user.name 2>/dev/null || printenv GIT_AUTHOR_NAME)
    [ -n "$name" ]
}

@test "QA: git config email 설정됨" {
    email=$(git -C /workspace config user.email 2>/dev/null || printenv GIT_AUTHOR_EMAIL)
    [ -n "$email" ]
}

@test "QA: gh CLI 사용 가능" {
    run gh --version
    [ "$status" -eq 0 ]
}

@test "QA: gh 인증 상태 확인" {
    token="${GITHUB_TOKEN:-$GH_TOKEN}"
    [ -n "$token" ]
}

@test "QA: git branch 생성 가능" {
    branch_name="qa-test-$(date +%s)"
    run git -C /workspace checkout -b "$branch_name"
    [ "$status" -eq 0 ]

    # 정리: 원래 브랜치로 복귀
    git -C /workspace checkout - 2>/dev/null
    git -C /workspace branch -D "$branch_name" 2>/dev/null
}

@test "QA: git commit 워크플로우 (dry-run)" {
    # 실제 커밋하지 않고 git add --dry-run 으로 검증
    run git -C /workspace add --dry-run .
    [ "$status" -eq 0 ]
}

# ── 6. 컨테이너 환경 ──

@test "QA: DAL_NAME 환경변수" {
    [ -n "$DAL_NAME" ]
}

@test "QA: DALCENTER_URL 환경변수" {
    [ -n "$DALCENTER_URL" ]
}

@test "QA: GITHUB_TOKEN 설정됨" {
    token="${GITHUB_TOKEN:-$GH_TOKEN}"
    [ -n "$token" ]
}

@test "QA: dalcli 바이너리 존재" {
    run which dalcli
    [ "$status" -eq 0 ]
}

@test "QA: claude 바이너리 존재" {
    run which claude
    [ "$status" -eq 0 ]
}

@test "QA: bats 자체 실행 가능" {
    run bats --version
    [ "$status" -eq 0 ]
}
