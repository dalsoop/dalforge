uuid:    "scaler-01"
name:    "scaler"
description: "dalcenter 비대화 감지 및 분리 제안 — 6개 지표 모니터링, 임계치 초과 시 분리안 제시"
version: "1.0.0"
player:  "claude"
model:   "sonnet"
role:    "member"
skills:  ["skills/pre-flight"]
hooks:   []
auto_task: """
	[비대화 감지 — 6개 지표 수집 및 임계치 비교]

	echo "=== dalcenter scaler report ==="

	# 1. Go 코드 줄 수 (임계치: 50000)
	GO_LINES=$(find /workspace -name '*.go' | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')
	if [ "$GO_LINES" -ge 50000 ]; then echo "WARN: Go lines = $GO_LINES (threshold: 50000)"; else echo "OK: Go lines = $GO_LINES"; fi

	# 2. .dal/ 설정 파일 수 (임계치: 200)
	DAL_FILES=$(find /workspace/.dal -type f 2>/dev/null | wc -l)
	if [ "$DAL_FILES" -ge 200 ]; then echo "WARN: .dal files = $DAL_FILES (threshold: 200)"; else echo "OK: .dal files = $DAL_FILES"; fi

	# 3. 동시 running dal 수 (임계치: LXC 메모리 70% 기준)
	RUNNING_DALS=$(docker ps --filter label=dal --format json 2>/dev/null | wc -l)
	TOTAL_MEM_KB=$(grep MemTotal /proc/meminfo | awk '{print $2}')
	MEM_THRESHOLD=$((TOTAL_MEM_KB * 70 / 100 / 1048576))
	echo "INFO: running dals = $RUNNING_DALS (mem-based threshold: ~${MEM_THRESHOLD})"

	# 4. 열린 이슈 수 (임계치: 50)
	OPEN_ISSUES=$(gh issue list --state open --limit 100 --json number 2>/dev/null | python3 -c 'import sys,json; print(len(json.load(sys.stdin)))' 2>/dev/null || echo 0)
	if [ "$OPEN_ISSUES" -ge 50 ]; then echo "WARN: open issues = $OPEN_ISSUES (threshold: 50)"; else echo "OK: open issues = $OPEN_ISSUES"; fi

	# 5. 팀 수 (임계치: 12)
	TEAMS=$(systemctl list-units 'dalcenter@*' --no-legend 2>/dev/null | wc -l)
	if [ "$TEAMS" -ge 12 ]; then echo "WARN: teams = $TEAMS (threshold: 12)"; else echo "OK: teams = $TEAMS"; fi

	# 6. 빌드 시간 (임계치: 120초)
	BUILD_START=$(date +%s)
	go build -o /dev/null ./cmd/dalcenter/ 2>/dev/null
	BUILD_END=$(date +%s)
	BUILD_TIME=$((BUILD_END - BUILD_START))
	if [ "$BUILD_TIME" -ge 120 ]; then echo "WARN: build time = ${BUILD_TIME}s (threshold: 120s)"; else echo "OK: build time = ${BUILD_TIME}s"; fi

	# WARNING 요약
	WARNS=""
	[ "$GO_LINES" -ge 50000 ] && WARNS="${WARNS} go-lines"
	[ "$DAL_FILES" -ge 200 ] && WARNS="${WARNS} dal-files"
	[ "$OPEN_ISSUES" -ge 50 ] && WARNS="${WARNS} open-issues"
	[ "$TEAMS" -ge 12 ] && WARNS="${WARNS} teams"
	[ "$BUILD_TIME" -ge 120 ] && WARNS="${WARNS} build-time"

	if [ -n "$WARNS" ]; then
	  echo "WARNING: 임계치 초과 항목:${WARNS}"
	  echo "분리 분석을 수행합니다."
	else
	  echo "ALL OK — 임계치 초과 항목 없음"
	fi

	echo "=== end report ==="
	"""
auto_interval: "24h"
git: {
	user:         "dal-scaler"
	email:        "dal-scaler@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
