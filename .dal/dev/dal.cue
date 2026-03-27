uuid:    "dev-20260326"
name:    "dev"
version: "1.0.0"
player:  "claude"
role:    "member"
skills:  ["skills/go-review", "skills/docker-ops", "skills/mattermost-api", "skills/cue-schema", "skills/git-workflow", "skills/reviewer-protocol", "skills/inbox-protocol", "skills/history-hygiene", "skills/escalation"]
hooks:   []
git: {
    user:         "dal-${name}"
    email:        "dal-${name}@dalcenter.local"
    github_token: "env:GITHUB_TOKEN"
}
