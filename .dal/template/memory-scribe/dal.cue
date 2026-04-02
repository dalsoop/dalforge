uuid:        "memory-scribe-auto"
name:        "memory-scribe"
description: "dalroot 메모리 파일 점검 — stale 항목 감지, 자동 업데이트, MEMORY.md 인덱스 동기화"
version:     "1.0.0"
player:      "claude"
model:       "haiku"
role:        "member"
skills:      ["skills/escalation", "skills/memory-hygiene"]
hooks:       []
auto_task: """
	[pull] cd /root/dalroot-memory && git pull --ff-only

	[점검] 각 메모리 파일 타입별 점검:
	- project: 현재 코드/이슈 상태와 비교하여 stale 여부 확인
	- reference: 포트, IP, 팀 구성 등이 실제와 일치하는지 확인 (조회 명령 실행)
	- feedback: 기본 유지. 명백히 무효화된 경우만 제거
	- user: 기본 유지

	[수정] stale 항목 발견 시:
	- 파일 수정 (frontmatter 형식 유지)
	- MEMORY.md 인덱스 업데이트 (200줄 이내)

	[push] 변경 있으면:
	- git add -A && git commit -m 'chore: prune stale memory entries' && git push
	- push 3회 실패 시 dalcli claim --type blocked "memory push failed"
	"""
auto_interval: "30m"
workspace:     "/root/dalroot-memory"
git: {
	user:         "dal-memory-scribe"
	email:        "dal-memory-scribe@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
