uuid:    "reviewer-20260326"
name:    "reviewer"
version: "1.0.0"
player:  "codex"
role:    "member"
skills:  ["skills/go-review", "skills/security-audit", "skills/docker-ops", "skills/git-workflow", "skills/reviewer-protocol", "skills/inbox-protocol", "skills/history-hygiene", "skills/escalation"]
hooks:   []
auto_task: "gh pr list --state open --json number,title,headRefName --jq '.[]' | head -5 확인. 새 PR 있으면 gh pr diff <number>로 코드 리뷰 수행. 보안 취약점, Go 관용구, 에러 처리, Docker 보안 관점으로 리뷰. 결과를 gh pr review <number> --comment으로 게시."
git: {
    user:         "dal-${name}"
    email:        "dal-${name}@dalcenter.local"
    github_token: "env:GITHUB_TOKEN"
}
