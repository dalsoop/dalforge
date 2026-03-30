uuid:    "dalroot-pve-20260329"
name:    "dalroot"
version: "1.0.0"
player:  "claude"
role:    "leader"
model:   "opus"
skills:  ["skills/pre-flight", "skills/escalation", "skills/inbox-protocol", "skills/credential-ops", "skills/deploy-binary", "skills/health-check", "skills/soft-serve-ops", "skills/dal-lifecycle"]
hooks:   []
workspace: "host"
git: {
	user:         "dalroot"
	email:        "dalroot@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
