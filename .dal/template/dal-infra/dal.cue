uuid:    "dal-infra-01"
name:    "dal-infra"
version: "1.0.0"
player:  "claude"
role:    "member"
skills:  ["skills/git-workflow", "skills/pre-flight"]
hooks:   []
git: {
	user:         "dal-infra"
	email:        "dal-infra@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
