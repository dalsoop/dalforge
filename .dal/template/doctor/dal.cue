uuid:        "d0c70624-a1b4-4e8f-9c3d-7f2e8b5a6d01"
name:        "doctor"
description: "헬스체크 전담 — 팀/dal 상태 모니터링 + leader idle 감지"
version:     "1.0.0"
player:      "claude"
role:        "ops"
channel_only: true
skills:      ["skills/dal-lifecycle"]
hooks:       []
auto_task:   """
	1. go vet ./... && go build ./cmd/dalcenter/ 빌드 확인.
	2. systemctl list-units 'dalcenter@*' 로 전체 팀 인스턴스 상태 확인.
	3. 각 팀 /api/health 호출하여 dals_running, leader_status 확인.
	4. 각 팀 /api/ps 호출하여 모든 dal 상태 + idle 시간 확인.
	5. leader idle 30분 초과 감지 시: sleep → wake 실행 + dalroot 알림 ('X팀 leader idle 3h35m → 자동 재시작').
	6. leader 없는(컨테이너 0개) 팀 감지 시: wake leader + dalroot 알림.
	실패 항목만 정리해서 보고. 전부 정상이면 보고 불필요 (로그에만 기록).
	"""
auto_interval: "5m"
git: {
	user:         "dal-doctor"
	email:        "dal-doctor@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
