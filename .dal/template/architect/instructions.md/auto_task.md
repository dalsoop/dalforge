# architect auto_task — 1시간 주기 실행

## 실행 순서

### 1. 열린 이슈 스캔

```bash
gh issue list --repo dalsoop/dalcenter --state open --json number,title,assignees,labels,createdAt
```

- 미할당 이슈 감지 → 설계 코멘트 작성 + leader에게 assign 지시
- 72시간+ 미진행 이슈 → leader에게 리마인드
- 신규 이슈 → 역할 1 (이슈 분석 → 설계) 수행

### 2. 열린 PR 스캔

```bash
gh pr list --repo dalsoop/dalcenter --state open --json number,title,author,reviews,createdAt,additions,deletions,files
```

- 72시간+ 리뷰 없는 PR → leader에게 리뷰 촉구
- auto_merge 조건 충족 PR → 자동 squash merge 실행
- require_human 조건 해당 PR → dal-control 채널에 사람 승인 요청
- 충돌 PR → 작성자 dal에게 리베이스 지시 (leader 경유)

### 3. 팀 상태 분석

```bash
dalcli-leader ps
```

- 과부하 dal 감지 (동시 3개+ 작업) → 재분배 또는 새 dal 제안
- 유휴 dal 감지 → 미할당 이슈 매칭
- 에러 상태 dal → 원인 분석 + leader에게 복구 지시

### 4. 최근 머지 PR 분석

```bash
gh pr list --repo dalsoop/dalcenter --state merged --json number,title,files,mergedAt --limit 10
```

- 후속 작업 누락 감지:
  - 코드 변경인데 테스트 없음 → 테스트 이슈 생성
  - API 변경인데 문서 미갱신 → 문서 이슈 생성
  - 관련 이슈 미닫힘 → 이슈 닫기 또는 후속 이슈 생성

### 5. 종합 판단 → 액션 실행

위 분석 결과를 종합하여:
1. 긴급: 장애/보안 → 즉시 leader에게 지시
2. 중요: 미할당 이슈, 방치 PR → leader에게 assign/리뷰 지시
3. 개선: 팀 구성 최적화, 프로세스 개선 → 이슈 생성
4. 보고: dal-control 채널에 요약 보고 (변경 사항이 있을 때만)

## 보고 채널

- 긴급/중요 → dal-control 채널 즉시 보고
- 일일 요약 → dal-daily 채널 (24시간 주기)
- 감사 결과 → GitHub 이슈 (label: `architect/audit`)
- 설계 결정 → 이슈 코멘트 + decisions inbox
