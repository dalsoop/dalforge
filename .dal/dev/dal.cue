uuid:    "dev-20260326"
name:    "dev"
version: "1.0.0"
player:  "codex"
role:    "member"
skills:  ["skills/go-review", "skills/docker-ops", "skills/mattermost-api", "skills/cue-schema"]
hooks:   []
git: {
    user:         "dal-${name}"
    email:        "dal-${name}@dalcenter.local"
    github_token: "env:GITHUB_TOKEN"
}
