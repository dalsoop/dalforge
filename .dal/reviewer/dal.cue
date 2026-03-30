uuid:    "reviewer-20260326"
name:    "reviewer"
version: "1.0.0"
player:  "codex"
role:    "member"
skills:  ["skills/go-review", "skills/security-audit", "skills/docker-ops", "skills/git-workflow", "skills/reviewer-protocol", "skills/inbox-protocol", "skills/history-hygiene", "skills/escalation"]
hooks:   []
auto_task:      "1. /workspace/CLAUDE.md 읽어서 프로젝트 컨벤션/규칙 파악. 2. gh pr list --state open --json number,title,headRefName --jq '.[]' | head -5 확인. 3. 새 PR 있으면 gh pr diff <number>로 변경사항 확인. 4. CLAUDE.md 컨벤션 기준 + 보안/Go관용구/에러처리/Docker보안 관점으로 리뷰. 5. 결과를 gh pr review <number> --comment으로 게시."
auto_interval:  "30m"
git: {
    user:         "dal-${name}"
    email:        "dal-${name}@dalcenter.local"
    github_token: "env:GITHUB_TOKEN"
}
