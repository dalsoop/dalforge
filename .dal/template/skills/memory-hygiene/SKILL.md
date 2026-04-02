# Memory Hygiene

dalroot 메모리 파일 점검 절차.

## 타입별 점검 기준

### project
- GitHub 이슈/PR 상태와 비교: 완료된 이슈 참조하는 메모리는 stale
- 코드 구조 변경 반영: 파일/디렉토리가 이동/삭제된 경우 업데이트
- 상대 날짜("다음 주", "목요일")가 남아있으면 절대 날짜로 변환 또는 제거

### reference
- 조회 명령 실행하여 실제 값과 비교 (하드코딩된 값 검증 아님)
- 경로가 존재하지 않으면 stale 처리
- 팀 구성 변경 반영: `systemctl list-units 'dalcenter@*'`로 확인

### feedback
- 기본 유지. 사용자 피드백은 함부로 삭제하지 않음
- 명백히 무효화된 경우만 제거 (예: 삭제된 기능에 대한 피드백)

### user
- 기본 유지

## MEMORY.md 인덱스 규칙
- 각 항목 1줄, 150자 이내
- 전체 200줄 이내 유지
- 파일 추가/삭제 시 인덱스 동기화

## 커밋 규칙
- 메시지: `chore: prune stale memory entries`
- force push, reset 금지
- push 3회 실패 시 escalation (dalcli claim)
