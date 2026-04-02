uuid:           "memory-scribe-auto"
name:           "memory-scribe"
version:        "1.0.0"
player:         "claude"
model:          "haiku"
role:           "member"
skills:         []
hooks:          []
auto_task:      "1. cd /root/dalroot-memory && git pull --ff-only. 2. MEMORY.md 읽고 각 메모리 파일 점검: project 타입은 현재 코드/이슈 상태와 비교하여 stale 여부 확인, reference 타입은 실제 값(포트, IP, 팀 구성 등)과 일치하는지 확인, feedback 타입은 기본 유지. 3. stale 항목 발견 시 파일 수정 + MEMORY.md 인덱스 업데이트. 4. 변경 있으면 git add -A && git commit -m 'chore: update stale memories' && git push."
auto_interval:  "30m"
git: {
	user:         "dal-memory-scribe"
	email:        "dal-memory-scribe@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
