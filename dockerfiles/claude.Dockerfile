FROM ubuntu:24.04

RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends \
      bash git curl ca-certificates nodejs npm && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code

# dalcli will be copied in at wake time
RUN mkdir -p /root/.claude/skills /root/.claude/hooks

ENV DAL_ROLE=member
ENV DAL_PLAYER=claude

WORKDIR /root
CMD ["sleep", "infinity"]
