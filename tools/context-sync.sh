#!/bin/bash
# context-sync: Claude Code 대화를 md로 추출 → soft-serve에 동기화
# dalcenter serve에서 주기적으로 호출

EXTRACT_CMD="claude-extract"
OUTPUT_DIR="${DALCENTER_CONTEXT_DIR:-/tmp/dalcenter-context}"
REPO_DIR="${DALCENTER_REPO:-$(pwd)}"

mkdir -p "$OUTPUT_DIR"

# 최근 세션 추출
$EXTRACT_CMD --recent 1 --output "$OUTPUT_DIR" --format markdown 2>/dev/null

# .dal/context/ 에 복사 → git sync
CONTEXT_DIR="$REPO_DIR/.dal/context"
mkdir -p "$CONTEXT_DIR"
cp "$OUTPUT_DIR"/*.md "$CONTEXT_DIR/" 2>/dev/null

# 변경사항 있으면 커밋
cd "$REPO_DIR"
if [ -n "$(git status --porcelain .dal/context/)" ]; then
    git add .dal/context/
    git commit -m "auto: context sync $(date +%Y-%m-%d-%H%M)" --no-gpg-sign 2>/dev/null
    echo "[context-sync] updated"
else
    echo "[context-sync] no changes"
fi
