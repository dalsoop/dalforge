# Wisdom

팀 공유 교훈. 모든 dal은 작업 전에 이 파일을 읽는다.

## Patterns

검증된 접근 방식.

## Anti-Patterns

피해야 할 것.

### 부분 완료로 이슈 닫기 금지

이슈의 done criteria가 전부 충족되지 않은 상태에서 이슈를 닫지 않는다.
PR 머지만으로 "완료"로 간주하지 않는다. 배포, 동작 확인, 테스트 등 모든 체크리스트를 확인한 뒤에만 닫는다.
부분 완료인 경우 남은 항목을 새 이슈로 분리하고 원본 이슈에 연결한다.

### Scope 없는 확장 금지

작업 중 발견한 파생 문제를 즉시 해결하려 하지 않는다. 이슈만 만들고 현재 작업을 먼저 완료한다.
leader가 scope chain을 적용하여 "이건 지금 범위 밖"을 판단한다.
2026-04-02 교훈: Grafana 레시피 1개 → PR 100개 폭발. scope 제한이 없었기 때문.

### 한 이슈에 PR 여러 개 금지

같은 이슈에 PR을 2개 이상 만들지 않는다. 수정이 필요하면 기존 PR에 커밋을 추가한다.
2026-04-02 교훈: #679(tell 멘션)에 PR 3개(#681 #682 #684), #577(bridge 통일)에 PR 2개(#687 #688) 생성 — 토큰 낭비.

### dal 템플릿만 만들고 검증 안 하기 금지

dal 템플릿을 생성하면 반드시 wake하여 실제 동작을 확인한다. 템플릿만 만들고 잠들어 있으면 의미 없음.
2026-04-02 교훈: 14종 템플릿 생성했지만 실제 가동+검증된 건 3-4개.

### config-manager 동기화 폭탄 주의

config-manager가 30분마다 전체 팀 레포에 PR을 생성할 수 있음.
- 동기화 PR은 하루 최대 1개/레포
- 변경이 없으면 PR 생성하지 않음
- PR 생성 전 기존 동기화 PR 확인 — 있으면 추가 커밋으로

### dalcli 자동 이슈 생성 주의

cmd/dalcli/cmd_run.go의 createGitHubIssue()가 검증 실패 시 이슈를 자동 생성함.
scope chain 우회 경로. 이슈 자동 생성은 leader/architect 승인 없이 발생.
- 자동 생성 이슈는 [auto] prefix로 식별 가능
- 과도하면 비활성화 검토

### memory-scribe main 직접 push 주의

memory-scribe가 git push를 직접 실행. main에 직접 커밋될 수 있음.
- 브랜치에서 작업 후 PR로 머지해야 함
- main 직접 push는 운영 정책 변경(scope chain 등) 시에만 예외 허용

### scaler go build 낭비

scaler auto_task에서 빌드 시간 측정을 위해 go build를 매일 실행.
- 실제 배포가 아닌 측정용이라 /dev/null로 버림
- 토큰+CPU 낭비. 빌드 시간은 CI 로그에서 확인하는 게 효율적

### scheduled_dalroot 자동 머지 주의

scheduled_dalroot가 LGTM PR을 leader에게 머지 지시함.
architect의 auto_merge 정책(additions < 100 + reviewer approve)과 충돌 가능.
- scheduled_dalroot는 머지 지시만, 실제 머지 판단은 leader가 scope chain 기준으로

### ops_watcher 무한 wake 주의

ops_watcher가 2분마다 dal 0인 팀에 leader wake 시도.
rate limit 없어서 실패 시 2분마다 반복 wake 요청 → 로그 폭탄.
- 연속 실패 3회 이상이면 alerting만 하고 wake 중단해야 함

### 다른 팀 레포 scope chain 미적용

dalcenter 레포에만 scope chain 적용됨. 다른 팀 레포(bridge-of-gaya-script, dal-qa-team, proxmox-host-setup)에는 없음.
config-manager가 동기화해야 하지만 아직 미실행.
- config-manager 가동 시 자동 배포 예정

### .gitignore 필수 항목

모든 레포에 다음을 .gitignore에 포함해야 함:
- .claude/worktrees/ — agent 임시 워크트리
- now.md — dal 런타임 임시 파일
- review-cache/ — reviewer 캐시
- .dal/data/ — 런타임 데이터 (tasks.json, escalations.json, feedback.json 등)
- .dal/*/history.md — dal 개인 히스토리

이것들이 커밋되면 레포가 불필요하게 비대해지고, 충돌이 발생함.
2026-04-02 교훈: PHS 레포에 worktrees 22,000줄 커밋, dal-qa-team에 review-cache 서브모듈 생성.

### remote branch 정리

dalcenter에 180개 remote branch 누적. 머지 후 자동 삭제 설정 필요.
GitHub repo settings > Automatically delete head branches 활성화.
