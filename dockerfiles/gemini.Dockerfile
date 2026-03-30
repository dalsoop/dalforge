FROM ubuntu:24.04

RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends \
      bash git curl ca-certificates python3 python3-pip nodejs npm && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN pip3 install --break-system-packages gemini-cli || true

RUN mkdir -p /root/.gemini/skills

# Quorum — multi-agent consensus & orchestration
RUN npm install -g quorum

ENV DAL_ROLE=member
ENV DAL_PLAYER=gemini

WORKDIR /root
CMD ["sleep", "infinity"]
