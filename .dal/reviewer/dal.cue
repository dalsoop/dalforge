uuid:    "reviewer-20260326"
name:    "reviewer"
version: "1.0.0"
player:  "codex"
role:    "member"
skills:  ["skills/go-review", "skills/security-audit", "skills/docker-ops", "skills/git-workflow", "skills/reviewer-protocol", "skills/inbox-protocol", "skills/history-hygiene", "skills/escalation"]
hooks:   []
git: {
    user:         "dal-${name}"
    email:        "dal-${name}@dalcenter.local"
    github_token: "env:GITHUB_TOKEN"
}
