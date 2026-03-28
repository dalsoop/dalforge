uuid:    "leader-20260326"
name:    "leader"
version: "1.0.0"
player:  "claude"
role:    "leader"
skills:  ["skills/go-review", "skills/docker-ops", "skills/security-audit", "skills/pre-flight", "skills/reviewer-protocol", "skills/inbox-protocol", "skills/history-hygiene", "skills/escalation", "skills/leader-protocol"]
hooks:   []
git: {
    user:         "dal-${name}"
    email:        "dal-${name}@dalcenter.local"
    github_token: "env:GITHUB_TOKEN"
}
