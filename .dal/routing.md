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
| CLI 명령어 등록 | dal-infra | Single |
| dalroot 알림/파이프라인 | dal-infra | Single |
| systemd/matterbridge 설정 | dal-infra | Single |
| 문서 최신화 (README 등) | dal-infra | Single |
| dalroot tell 처리 | leader (분석) → 해당 member | Single/Multi |
| 외부 레포 PR 수정 | dev | Single |
| 외부 레포 .dal/ 구성 | dev | Single |
| 바이너리 빌드 | dev | Single |
| 바이너리 배포 | dev → dalcenter self-update | Single |
| 스킬 갭 | → 에스컬레이션 | — |

## Multi 모드 downstream
- dev assign → tester 동시 wake
- 코드 변경 → verifier 동시 wake
