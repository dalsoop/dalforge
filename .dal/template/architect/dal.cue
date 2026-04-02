uuid:    "architect-01"
name:    "architect"
description: "설계자+감사자 — 이슈 분석, 팀 구성, PR 승인 정책, 시스템 감사, 놓친 것 탐지"
version: "1.0.0"
player:  "claude"
model:   "opus"
role:    "leader"
skills:  ["skills/go-review", "skills/security-audit", "skills/pre-flight", "skills/reviewer-protocol", "skills/inbox-protocol", "skills/leader-protocol", "skills/escalation", "skills/history-hygiene"]
hooks:   []
auto_task:      "instructions.md/auto_task.md 참조"
auto_interval:  "1h"
git: {
	user:         "dal-architect"
	email:        "dal-architect@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
