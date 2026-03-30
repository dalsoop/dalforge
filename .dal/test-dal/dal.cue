uuid:           "test-dal-20260330"
name:           "test-dal"
version:        "1.0.0"
player:         "claude"
role:           "member"
skills:         ["skills/git-workflow", "skills/docker-ops", "skills/escalation"]
hooks:          []
auto_task:      "tests/smoke-qa.bats 실행. 실패 항목 있으면 dalcli report로 보고하고 gh issue create로 이슈 생성. 전부 PASS면 보고 불필요."
auto_interval:  "1h"
git: {
    user:         "dal-${name}"
    email:        "dal-${name}@dalcenter.local"
    github_token: "env:GITHUB_TOKEN"
}
