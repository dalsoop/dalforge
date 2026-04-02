uuid:    "cfg-mgr-01"
name:    "config-manager"
description: "설정 동기화 — template 변경 감지, 팀 레포 PR, 도구 설치 감사"
version: "1.0.0"
player:  "claude"
model:   "haiku"
role:    "member"
skills:  ["skills/git-workflow", "skills/pre-flight"]
hooks:   []
auto_task:      "1. .dal/template/ git diff 감지 → 변경된 charter/skill/schema를 전체 팀 레포에 동기화 PR 생성. 2. 각 팀 charter에 참조된 도구(ccw 등) 설치 여부 확인. 3. 불일치 감지 시 이슈 생성 + 자동 수정 PR."
auto_interval:  "30m"
git: {
	user:         "dal-config"
	email:        "dal-config@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
