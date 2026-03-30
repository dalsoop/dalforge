FROM ubuntu:24.04

RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends \
      bash git curl ca-certificates python3 python3-pip build-essential && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Node.js 22 (nodesource)
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y -qq nodejs && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN pip3 install --break-system-packages gemini-cli || true

RUN mkdir -p /root/.gemini/skills
# CCW — JSON-driven multi-agent workflow orchestration
RUN npm install -g claude-code-workflow && ccw install -m Global || true

ENV DAL_ROLE=member
ENV DAL_PLAYER=gemini

WORKDIR /root
CMD ["sleep", "infinity"]
