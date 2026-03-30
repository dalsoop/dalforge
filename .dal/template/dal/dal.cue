uuid:    "99ab0934-07fb-423f-84e6-1ce8aef91919"
name:    "dal"
version: "1.0.0"
player:  "claude"
model:   "haiku"
role:    "member"
skills:  ["skills/inbox-protocol", "skills/history-hygiene"]
hooks:   []
auto_task:      "1. /workspace/decisions/inbox/ → decisions.md 병합 (중복 제거 후 삭제). 2. /workspace/history-buffer/ → .dal/{name}/history.md 병합. 3. /workspace/wisdom-inbox/ → wisdom.md 병합. 4. history.md 12KB 초과 시 압축. 5. decisions.md 50KB 초과 시 30일+ 아카이브. 6. README.md, CLAUDE.md 갱신 필요 시 ccw tool update_module_claude로 자동 생성. 7. 변경 시 git add + commit + push."
auto_interval:  "30m"
git: {
	user:         "dal-docs"
	email:        "dal-docs@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
