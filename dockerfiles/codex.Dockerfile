FROM ubuntu:24.04

RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends \
      bash git curl ca-certificates nodejs npm && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN npm install -g @openai/codex

RUN mkdir -p /root/.codex/skills

ENV DAL_ROLE=member
ENV DAL_PLAYER=codex

WORKDIR /root
CMD ["sleep", "infinity"]
