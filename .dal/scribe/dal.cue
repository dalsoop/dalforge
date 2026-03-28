uuid:           "scribe-auto"
name:           "scribe"
version:        "1.0.0"
player:         "claude"
model:          "haiku"
role:           "member"
skills:         ["skills/history-hygiene", "skills/inbox-protocol"]
hooks:          []
auto_task:      "1. /workspace/decisions/inbox/ 파일 → decisions.md 병합 (중복 제거 후 삭제). 2. 각 dal /workspace/history-buffer/ → .dal/{name}/history.md 병합. 3. /workspace/wisdom-inbox/ → wisdom.md 병합. 4. history.md 12KB 초과 시 Core Context 압축. 5. decisions.md 50KB 초과 시 30일+ 항목 archive. 6. 변경 시 git add + commit + push."
auto_interval:  "30m"
git: {
	user:         "dal-scribe"
	email:        "dal-scribe@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
