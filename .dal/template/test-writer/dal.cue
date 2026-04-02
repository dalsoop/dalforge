uuid:        "test-writer-01"
name:        "test-writer"
description: "PR별 테스트 코드 자동 작성 — 변경 코드 분석, _test.go 생성"
version:     "1.0.0"
player:      "claude"
role:        "member"
skills:      ["skills/git-workflow"]
hooks:       []
auto_task:      "1. gh pr list --state open 스캔. 2. PR diff에서 테스트 없는 .go 파일 감지 (_test.go 미존재). 3. 해당 파일에 대한 _test.go 작성. 4. PR 브랜치에 커밋 또는 리뷰 코멘트로 제안."
auto_interval:  "30m"
git: {
	user:         "dal-test-writer"
	email:        "dal-test-writer@dalcenter.local"
	github_token: "env:GITHUB_TOKEN"
}
