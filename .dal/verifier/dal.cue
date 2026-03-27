uuid:           "verifier-20260326"
name:           "verifier"
version:        "1.0.0"
player:         "claude"
player_version: "go"
role:           "member"
skills:         ["skills/go-review", "skills/go-ci", "skills/test-strategy", "skills/security-audit", "skills/git-workflow", "skills/reviewer-protocol", "skills/inbox-protocol", "skills/history-hygiene", "skills/escalation"]
hooks:          []
auto_task:      "dalcenter 자체 검증: go vet ./... && go test ./... && go build ./cmd/dalcenter/ && go build ./cmd/dalcli/ 실행. 실패 시 실패 항목과 에러 내용을 정리해서 보고. 전부 통과하면 PASS 한줄로 보고."
auto_interval:  "1h"
git: {
    user:         "dal-${name}"
    email:        "dal-${name}@dalcenter.local"
    github_token: "env:GITHUB_TOKEN"
}
