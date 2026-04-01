---
id: DAL:SKILL:a7c3e901
---
# Infra Service Request (인프라 서비스 요청 처리)

dalroot가 사용자의 인프라 서비스 요청(레시피 추가, 호스트 설정 변경 등)을 처리하는 프로토콜.

## 트리거

사용자가 다음을 요청할 때:
- Proxmox 호스트에 새 서비스 설치 (grafana, loki, prometheus 등)
- PHS 레시피 추가/수정
- 호스트 레벨 설정 변경

## 워크플로우

```
1. 사용자 요청 수신
2. dalsoop/proxmox-host-setup 레포에 이슈 생성
   - gh issue create -R dalsoop/proxmox-host-setup --title "..." --body "..."
3. PHS dal 팀에 작업 지시
   - pct exec $CT -- dalcenter tell proxmox-host-setup "이슈 #N 작업 시작해"
4. PHS dal 팀이 구현 + PR 생성
5. dalroot가 PR 리뷰 + 머지
6. 결과를 사용자에게 보고
```

## 핵심 규칙

1. **이슈 생성만으로 끝내지 않는다** -- 반드시 `dalcenter tell`로 팀 지시까지 완료
2. 이슈에 요청 배경, 대상 호스트, 원하는 구성을 명시
3. PHS 팀 리더가 적절한 dal에게 assign하므로, dalroot는 팀 레벨로만 지시
4. PR 머지 후 실제 적용은 별도 단계 -- 호스트에서 레시피 실행 필요

## 대상 레포

| 레포 | 용도 |
|------|------|
| dalsoop/proxmox-host-setup | Proxmox 호스트 자동화 레시피 |

## 참고

- wisdom.md Pattern #4: "PHS 레시피 요청은 이슈 생성 + dal 팀 지시까지 완료해야 함"
