uuid:    "c9380496-8203-44e4-9025-6e624e95cb67"
name:    "dalops"
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
