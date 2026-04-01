uuid:    "c9380496-8203-44e4-9025-6e624e95cb67"
name:    "dalops"
description: "CCW 기반 오케스트레이터 — 코드 구현, 리뷰, 테스트 워크플로우"
version: "1.0.0"
player:  "claude"
role:    "ops"
channel_only: true
skills:  ["skills/git-workflow", "skills/pre-flight"]
hooks:   []
git: {
	user:         "dal-ops"
	email:        "dal-ops@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
