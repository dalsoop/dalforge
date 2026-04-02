uuid:    "standup-manager-01"
name:    "standup-manager"
description: "일일 스탠드업 수집 및 보고 — 전체 팀 업무 현황 종합, 주간 요약"
version: "1.0.0"
player:  "claude"
model:   "haiku"
role:    "member"
skills:  ["skills/pre-flight"]
hooks:   []
// cron trigger: 0 9 * * * (auto_interval 대신 cron 사용)
auto_task: """
	[09:00 일일 스탠드업 수집]
	1. 전체 9개 팀 leader에게 업무 보고 요청
	   - dalcenter tell <team> "스탠드업: 어제 완료, 오늘 계획, 블로커를 보고해주세요"
	   - fallback: /api/tasks (최근 24시간 done task), GitHub API (merged PR, closed issue)
	2. 30분 대기 후 응답 수집

	[09:30 종합 보고서]
	3. dal-daily 채널(id: tk85m97hf7b73mn8rr3kwxt6fw)에 보고서 포스팅
	   - 포맷: 팀별 완료/진행/블로커 정리
	4. 미보고 팀에 리마인드 전송

	[월요일 09:30 주간 요약]
	5. 주간 요약 생성 — 팀별 생산성 트렌드, 반복 블로커, 완료율
	6. dal-daily 채널에 주간 리포트 포스팅
	"""
git: {
	user:         "dal-standup-manager"
	email:        "dal-standup-manager@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
