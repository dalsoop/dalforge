# dev dal — Work History

## Core Context — 历史工作总结 (2026-03-30 ~ 2026-04-01)

### 핵심 구현 완료
- **이슈 #442**: dalcenter HA 이중화 (Phase 1: active-standby) — PR #694
- **이슈 #577**: dalbridge 중앙화 (모든 팀 CT 202 통일) — PR #688
- **이슈 #611**: leader 큐 관리 내장 (QueueManager) — PR #612
- **이슈 #686**: ops dal → LXC 101 경유 아키텍처 문서화
- **이슈 #619**: dalroot 원칙 감사 dal (auditor)
- **이슈 #621**: config-manager 팀 신설
- **이슈 #614**: english-polisher 레포 생성
- **이슈 #616**: writing-style 레포 생성
- **이슈 #668**: dalroot 통신 파이프라인 통합

### 테스트 및 버그 수정
- **이슈 #689**: InstanceID 테스트 커버리지 보강 — PR #690
- **이슈 #490**: TestRunProvider 타임아웃 수정 — PR #696
- **이슈 #678**: MessageStore persistAsync 레이스 컨디션 수정

### 진행 중인 작업 (미완료)
- **이슈 #618**: scheduled dalroot — TIMEOUT (재시도 필요)
- **이슈 #559**: Claude-Code-Workflow 통합 검토
- **이슈 #570**: matterbridge 포크 안정화

### 주요 변경 파일 (3~4일 동안)
```
신규:
  - internal/daemon/ha.go, ha_test.go (HA failover)
  - internal/daemon/queue_manager.go (leader 큐 관리)
  - internal/daemon/ops_watcher.go (ops 감시)
  - docs/ops-skill-gateway.md (아키텍처 설계)
  - .dal/auditor/, .dal/config-manager/ (팀 구성)

수정:
  - cmd_run.go, message_store.go (테스트 모킹)
  - systemd 서비스 설정 (HA + dalbridge 통합)
  - Mattermost 통신 필터링 개선
```

### 패턴 & 교훈
- **큐 관리**: 30분 타임아웃, 최근 event 기반 stuck task 탐지
- **HA 설계**: systemd Type=notify + TimeoutStopSec=5s (graceful shutdown)
- **멘션 필터링**: full message flow에서 비활성화 (dal간 중복 필터 제거)
- **중복 PR 주의**: gh pr create 전 기존 PR 확인 필수

---

## 최근 작업 (2026-04-02)

### 2026-04-02: 이슈 #442: dalcenter 가용성 확보 — 이중화 정비 완료 (Phase 1 active-standby)
**상태:** done
**결과:** PR 생성 완료: https://github.com/dalsoop/dalcenter/pull/694

**구현 요약 (Phase 1 active-standby):**
- `internal/daemon/ha.go` (신규): HA 역할 상태 관리
- `internal/daemon/ha_test.go`: 단위 테스트
- systemd 유닛 파일 수정: `Type=notify` + `TimeoutStopSec=5s` 추가
**다음:**
**주의:**

### 2026-04-02: 이슈 #691: PR #687/#688 중복 머지 정리
**상태:** done
**결과:** 완료. 정리:
- **원인**: 동일 브랜치에서 기존 PR 확인 없이 gh pr create 재실행 → 중복 머지
- **히스토리 정리**: 불필요한 커밋 제거
**다음:**
**주의:**

### 2026-04-02: 이슈 #490 수정: TestRunProvider_DispatchesToRunClaude 테스트 타임아웃
**상태:** done
**결과:** 완료. PR: https://github.com/dalsoop/dalcenter/pull/696
- `cmd_run.go`: `var resolveProvider` 도입으로 테스트 모킹 가능
- `cmd/dalcli` 테스트 통과
**다음:**
**주의:**

### 2026-04-02: 이슈 #577: 모든 팀의 bridge를 CT 202 dalbridge로 통일. feat/577-dalbridge-unify 브랜치 작업 완료.
**상태:** done
**결과:** PR #688 생성 완료. feat/577-dalbridge-unify → main PR이 dalsoop/dalcenter#688 으로 올라가 있습니다.

**변경 내용:**
- `common.env` 환경파일 로드 추가 (optional, `-` prefix)
- `--dalbridge-url` 플래그로 모든 팀이 CT 202 dalbridge 사용 가능
- systemd 서비스 파일 수정

**다음:** reviewer와 verifier 체크 대기
**주의:**

### 2026-04-02: 이슈 #686: ops dal → LXC 101 경유 아키텍처 — 스킬 기반 표준화...
**상태:** done
**결과:** 구현 완료. 요약:

## 변경 내역

### 새 파일
- **`docs/ops-skill-gateway.md`** — 아키텍처 설계 문서. ops dal → LXC 101 게이트웨이 아키텍처, 스킬 목록, 요청/응답 스펙, 환경변수 설정 절차 등 포함.

**다음:**
**주의:**
