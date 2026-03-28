# Routing

| 작업 유형 | 담당 | 모드 |
|---|---|---|
| Go 구현/버그 수정 | dev | Single |
| 코드 리뷰 | reviewer | Single |
| 테스트 작성 | tester | Single |
| 빌드/정적분석 | verifier | Single |
| PR 생성/머지 | leader | Direct |
| 아키텍처 결정 | leader | Direct |
| 구현+테스트 동시 | dev + tester | Multi |
| 스킬 갭 | → 에스컬레이션 | — |

## Multi 모드 downstream
- dev assign → tester 동시 wake
- 코드 변경 → verifier 동시 wake
