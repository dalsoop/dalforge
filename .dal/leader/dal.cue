uuid:    "leader-20260326"
name:    "leader"
version: "1.0.0"
player:  "codex"
role:    "leader"
skills:  ["skills/go-review", "skills/docker-ops", "skills/security-audit"]
hooks:   []
git: {
    user:         "dal-${name}"
    email:        "dal-${name}@dalcenter.local"
    github_token: "env:GITHUB_TOKEN"
}
