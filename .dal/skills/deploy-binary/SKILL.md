---
id: DAL:SKILL:depl0001
---
> **CT_ID**: dalcenter LXC의 VMID. 현재 기본값 `105`. 이중화 시 `125`.

# Deploy Binary — dalcenter 바이너리 배포

## 빌드 + 배포

```bash
# 1. 빌드 (호스트, dalcenter 레포에서)
cd /root/jeonghan/repository/dalcenter
go build -o /tmp/dalcenter-new ./cmd/dalcenter/

# 2. dalcli도 빌드 (컨테이너 안에서 사용)
go build -o /tmp/dalcli-new ./cmd/dalcli/
go build -o /tmp/dalcli-leader-new ./cmd/dalcli-leader/

# 3. LXC $CT로 전송
pct push $CT /tmp/dalcenter-new /usr/local/bin/dalcenter.new
pct push $CT /tmp/dalcli-new /usr/local/bin/dalcli.new
pct push $CT /tmp/dalcli-leader-new /usr/local/bin/dalcli-leader.new

# 4. 교체 (LXC $CT에서)
pct exec $CT -- bash -c '
cp /usr/local/bin/dalcenter /usr/local/bin/dalcenter.bak
mv /usr/local/bin/dalcenter.new /usr/local/bin/dalcenter
chmod +x /usr/local/bin/dalcenter
mv /usr/local/bin/dalcli.new /usr/local/bin/dalcli
mv /usr/local/bin/dalcli-leader.new /usr/local/bin/dalcli-leader
chmod +x /usr/local/bin/dalcli /usr/local/bin/dalcli-leader
'

# 5. 서비스 재시작
pct exec $CT -- systemctl restart \
  dalcenter@dalcenter \
  dalcenter@veilkey-selfhosted \
  dalcenter@veilkey-v2 \
  dalcenter@bridge-of-gaya-script \
  dalcenter@dal-qa-team

# 6. 확인
pct exec $CT -- systemctl is-active \
  dalcenter@dalcenter \
  dalcenter@veilkey-selfhosted \
  dalcenter@veilkey-v2 \
  dalcenter@bridge-of-gaya-script \
  dalcenter@dal-qa-team
```

## 롤백

```bash
pct exec $CT -- bash -c '
mv /usr/local/bin/dalcenter.bak /usr/local/bin/dalcenter
systemctl restart dalcenter@dalcenter dalcenter@veilkey-selfhosted dalcenter@veilkey-v2 dalcenter@bridge-of-gaya-script dalcenter@dal-qa-team
'
```

## 주의
- 재시작하면 running 중인 dal 컨테이너의 dalcli와는 별개 (dalcli는 Docker 이미지 안에 있음)
- dalcli를 업데이트하려면 `dalcenter image build` 후 컨테이너 재생성 필요
- emotion-ai는 systemd가 아닌 수동 프로세스로 별도 재시작 필요
