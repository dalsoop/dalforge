uuid:    "dc-codex-dev-20260327"
name:    "codex-dev"
version: "1.0.0"
player:  "codex"
role:    "member"
skills:  ["skills/git-workflow", "skills/reviewer-protocol", "skills/inbox-protocol", "skills/history-hygiene", "skills/escalation"]
hooks:   []
git: {
	user:         "dal-codex-dev"
	email:        "dal-codex-dev@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
