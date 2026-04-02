### 2026-04-02: 이슈 #686: ops dal → LXC 101 경유 아키텍처 — 스킬 기반 표준화...
**상태:** done
**결과:** 구현 완료. 요약:

## 변경 내역

### 새 파일
- **`docs/ops-skill-gateway.md`** — 아키텍처 설계 문서. ops dal → LXC 101 게이트웨이 아키텍처, 스킬 목록, 요청/응답 스펙, 환경변수 설정 절차 등 포함.

**다음:**
**주의:**

### 2026-04-02: PR #682 CI 실패 수정: TestTellCmd_LeaderMention_Direct가 실패합니다. c...
**상태:** done
**결과:** 수정 완료. 문제는 두 가지였습니다:

1. **auto-wake에서 `resolveRepoURL` 호출** — `--direct` 모드에서도 auto-wake가 실행되어 `DALCENTER_URLS`가 없으면 에러 (이전 회수 같음)
2. **TestTellCmd_LeaderMention_Direct 테스트** — direct 모드에서 `--no-bridge` 플래그를 인식하지 못해 테스트 실패.

**해결 방법:**
- `auto-wake`를 `--direct` 모드에서 스킵하도록 수정
- tell 커맨드에서 `--no-bridge` 플래그를 매개변수로 추가

**다음:**
**주의:**

### 2026-04-02: 이슈 #679: dalcenter tell 메시지에 @dal-leader 멘션 자동 포함. 브...
**상태:** done
**결과:** 커밋 `d4ff01b`가 이미 존재합니다. 이슈 #679의 변경사항이 모두 반영되어 있습니다.

변경 내용 확인:
- `cmd/dalcenter/cmd_tell.go` — `sendViaDalcenter`, `sendViaBridge` 두 경로 모두에서 `"@dal-leader "` 멘션 자동 추가
- 룩업: go build 성공, 관련 테스트 (TestTellCmd_LeaderMention 등) 통과

**다음:**
**주의:**

### 2026-04-02: 이슈 #676 관련 작업 2건:
1. PR #672 (issue-633/leader-charter-routing) CI 체크 통과
2. PR #673 (feat/done-criteria-655) feature 브랜치 상태 확인

**상태:** done
**결과:** 보고 완료했습니다.

**다음:**
**주의:**

### 2026-04-02: 이슈 #671: prelik.com 랜딩 콘텐츠 업데이트

## 작업 레포
dalsoop/landing-prelik — 사업영역 페이지 콘텐츠 추가
**상태:** done
**결과:** 완료. PR: https://github.com/dalsoop/landing-prelik/pull/2

**변경 요약:**

| 섹션 | 변경 내용 |
|------|----------|
| **사업영역** | SparkAgent, FlowForge, LensView, CastDeploy, ShieldSync 등 6개 제품 추가. 각 제품마다 icon, 설명, 주요 기능(bullet points) 포함 |

**다음:**
**주의:**

### 2026-04-02: 이슈 #670: landing 4개 사이트 한/영 다국어 지원 (i18n)

## 작업 레포
- dalsoop/landing-prelik — i18n 구현
- dalsoop/landing-dal-squad — i18n 구현
- dalsoop/landing-veilkey — i18n 구현
- dalsoop/landing-castdeploy — i18n 구현
**상태:** done
**결과:** 4개 레포 모두 i18n 작업이 완료되고 PR이 열려 있습니다.

## 현재 상태

| 레포 | 브랜치 | PR | 상태 |
|------|--------|-----|------|
| dalsoop/landing-prelik | `feat/i18n-670` | #2 | Open, 검수 대기 |
| dalsoop/landing-dal-squad | `feat/i18n-670` | #1 | Open, 검수 대기 |
| dalsoop/landing-veilkey | `feat/i18n-670` | #2 | Open, 검수 대기 |
| dalsoop/landing-castdeploy | `feat/i18n-670` | #3 | Open, 검수 대기 |

**다음:** 각 PR을 검수하고 머지하기 (4개 외부 레포)
**주의:**

### 2026-04-02: 이슈 #668: dalroot 통신 파이프라인 통합 — MM 기반 단일 객체...
**상태:** done
**결과:** ## 작업 완료 보고

이슈 #668의 모든 구현 사항이 확인되었습니다.
