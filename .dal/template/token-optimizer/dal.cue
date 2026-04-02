uuid:    "token-optimizer-01"
name:    "token-optimizer"
description: "토큰 사용량 분석 및 최적화 — 비용 추적, 이상치 감지, 주간 보고서"
version: "1.0.0"
player:  "claude"
model:   "haiku"
role:    "member"
skills:  ["skills/escalation"]
hooks:   []
auto_task:      "1. /api/costs에서 최근 1시간 토큰 사용량 수집. 2. 이상치 감지 — 평균 대비 2x 이상 사용한 task 식별. 3. 주간 보고서 생성 → dal-control 채널 포스팅."
auto_interval:  "1h"
git: {
	user:         "dal-token-optimizer"
	email:        "dal-token-optimizer@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
