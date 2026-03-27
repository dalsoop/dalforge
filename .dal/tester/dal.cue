uuid:    "tester-20260326"
name:    "tester"
version: "1.0.0"
player:  "codex"
role:    "member"
skills:  ["skills/go-review", "skills/test-strategy", "skills/docker-ops"]
hooks:   []
git: {
    user:         "dal-${name}"
    email:        "dal-${name}@dalcenter.local"
    github_token: "env:GITHUB_TOKEN"
}
