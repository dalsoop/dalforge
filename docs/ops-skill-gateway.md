# Ops Skill Gateway 아키텍처

## 개요

ops dal들이 외부 서비스(CF, GitHub, DNS)에 직접 접근하는 대신,
LXC 101을 중앙 게이트웨이로 사용하여 스킬 기반 요청만 전송하는 구조.

```
ops dal (dalcenter 컨테이너)
  │
  │  HTTP POST (Bearer token)
  ▼
LXC 101 — host-ops API gateway (:9100)
  │
  │  실제 외부 호출
  ▼
CF API / GitHub API / DNS / certbot / systemctl
```

## 문제

- ops dal마다 CF API Token, GITHUB_TOKEN 등을 개별 보유 → 토큰 분산
- 새 ops dal 추가 시마다 부트스트래핑 반복
- dal 컨테이너에서 PVE 호스트 직접 접근 불가 (CLAUDE.md 삽질 교훈 참고)

## 해결

- LXC 101이 모든 인증 정보를 중앙 관리
- ops dal은 토큰 없이 스킬 이름 + 파라미터만 전송
- LXC 101이 실제 외부 API 호출 수행 후 결과 반환

## dalcenter 측 구현

### 환경변수

| 변수 | 설명 | 예시 |
|------|------|------|
| `DALCENTER_HOSTOPS_URL` | LXC 101 API base URL | `http://10.0.0.101:9100` |
| `DALCENTER_HOSTOPS_TOKEN` | Bearer 인증 토큰 | (env에서만 관리) |

### 스킬 목록

| 스킬 | API 경로 | 파라미터 | 설명 |
|------|----------|----------|------|
| `cf-pages-deploy` | `POST /api/cf-pages/deploy` | project, directory | CF Pages 배포 |
| `dns-manage` | `POST /api/dns/manage` | action(create/delete), type, name, content | DNS 레코드 관리 |
| `git-push` | `POST /api/git/push` | repo, branch, remote | Git push 대행 |
| `cert-manage` | `POST /api/cert/manage` | action(issue/renew), domain | 인증서 관리 |
| `service-restart` | `POST /api/service/restart` | service | systemctl restart 대행 |

### 요청/응답 구조

```go
// HostOpsRequest — ops dal이 LXC 101에 보내는 요청
type HostOpsRequest struct {
    Skill  string            `json:"skill"`   // e.g. "cf-pages-deploy"
    Params map[string]string `json:"params"`  // 스킬별 파라미터
}

// HostOpsResponse — LXC 101이 반환하는 응답
type HostOpsResponse struct {
    OK      bool   `json:"ok"`
    Message string `json:"message,omitempty"`
    Output  string `json:"output,omitempty"`  // 실행 결과 (stdout)
    Error   string `json:"error,omitempty"`   // 에러 메시지
}
```

### 클라이언트 구조

```go
// HostOpsClient — LXC 101 API 호출 클라이언트
type HostOpsClient struct {
    baseURL string
    token   string
    http    *http.Client
}
```

- credential_ops.go의 HTTP bridge 패턴과 동일한 구조
- `DALCENTER_HOSTOPS_URL` + `DALCENTER_HOSTOPS_TOKEN` 환경변수로 설정
- 타임아웃 30초 (배포 등 오래 걸리는 작업 고려)

### dalcenter 내 통합

1. `Daemon` 구조체에 `hostops *HostOpsClient` 필드 추가
2. `POST /api/hostops` 엔드포인트로 dal이 dalcenter 경유 호출 가능
3. dal 컨테이너 → dalcenter API → LXC 101 API 체인

```
dal 컨테이너 (dalcli)
  │ POST /api/hostops {"skill":"cf-pages-deploy","params":{...}}
  ▼
dalcenter (daemon)
  │ POST /api/cf-pages/deploy {...}
  ▼
LXC 101 (host-ops gateway)
  │ cf-cli / gh / certbot ...
  ▼
외부 서비스
```

### 에러 처리

- LXC 101 연결 실패 → 로그 + 에러 반환 (재시도 없음, 호출자가 판단)
- 인증 실패 (401) → 로그 + 명확한 에러 메시지
- 스킬 실행 실패 → LXC 101이 반환한 에러를 그대로 전달

## LXC 101 측 (proxmox-host-ops 레포, 별도 구현)

- HTTP API 서버 (`:9100`)
- Bearer token 인증
- 각 스킬별 핸들러가 실제 CLI/API 호출 수행
- CF API Token, GITHUB_TOKEN 등은 101 환경변수에서만 관리
- 요청 로깅/감사 (JSON 로그)

## 마이그레이션

1단계: dalcenter에 hostops 클라이언트 + `/api/hostops` 엔드포인트 구현 (이 PR)
2단계: LXC 101에 host-ops API 서버 구현 (proxmox-host-ops 레포)
3단계: 기존 ops dal의 직접 호출을 hostops 스킬 호출로 전환
