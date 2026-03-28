# Decisions

팀 운영과 아키텍처에 관한 확정된 결정. 번복 시 이 문서를 먼저 갱신한다.

---

## 1. 역할 구조 확정 (dalroot / leader / member)

dalroot는 인프라(컨테이너, 네트워크, 볼륨)를 소유하고, leader는 라우팅과 판단을 담당하며, member는 실행만 한다.
3단 구조를 확정하여 권한 경계를 명확히 하고, 각 계층이 자기 범위를 넘지 않도록 한다.

> Issue #361

## 2. .dal/ read-only overlay

`.dal/` 디렉토리를 read-only overlay로 마운트하여 멤버가 설정 파일을 실수로 수정하는 것을 방지한다.
변경은 호스트(dalroot) 측에서만 이루어지고, 컨테이너 내부에서는 읽기 전용으로 접근한다.

> Issue #361

## 3. 공유기억 파일 레이아웃 (영구 → git, 휘발 → state)

영구 데이터(decisions.md, wisdom.md 등)는 git으로 추적하고, 휘발성 런타임 데이터(PID, 소켓, 임시 상태)는 `.dal/data/`에 배치하여 git 밖에서 관리한다.
불필요한 diff와 커밋 충돌을 방지하면서도 중요한 결정과 교훈은 영속적으로 보존한다.

> Issue #361

## 4. scribe dal 도입

문서 작성과 갱신을 전담하는 scribe dal을 두어, 다른 멤버가 문서 작업에 시간을 빼앗기지 않고 본업에 집중할 수 있게 한다.
CHANGELOG, README, 릴리스 노트 등 반복적 문서 작업은 scribe에게 assign한다.

> Issue #361

## 5. 작업 UUID 체계

각 작업에 고유 UUID를 부여하여 추적성을 확보한다.
assign, 완료, 리뷰 등 작업 생명주기 전체에서 동일한 ID로 참조할 수 있어 로그 추적과 상태 관리가 용이해진다.

> Issue #361

## 6. dalcenter 이중화

dalcenter 장애 시 멤버 관리가 전면 중단되는 것을 방지하기 위해 이중화를 구성한다.
primary 장애 시 secondary가 자동으로 승격되어 멤버 생명주기 관리를 이어받는다.

> Issue #361

---

## Inbox

_리뷰 대기 중인 제안을 여기에 추가한다._
