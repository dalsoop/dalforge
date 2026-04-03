FROM ubuntu:24.04

WORKDIR /root

RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends \
      bash git curl ca-certificates gpg build-essential && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Node.js 22 (nodesource)
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y -qq nodejs && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list && \
    apt-get update -qq && apt-get install -y -qq gh && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# dalcli and settings.json are injected at wake time
RUN mkdir -p .claude/skills .claude/hooks

# Git credential helper — uses GH_TOKEN env var for HTTPS push
RUN git config --global credential.helper '!f() { echo username=x-access-token; echo "password=$GH_TOKEN"; }; f'

# CCW — JSON-driven multi-agent workflow orchestration (git clone build)
RUN git clone --depth 1 https://github.com/catlog22/Claude-Code-Workflow.git /opt/ccw && \
    cd /opt/ccw && npm install --ignore-scripts && npx tsc && \
    printf '#!/bin/sh\nnode /opt/ccw/dist/index.js "$@"\n' > /usr/local/bin/ccw && \
    chmod +x /usr/local/bin/ccw && \
    ccw install -m Global || true

ENV DAL_ROLE=member
ENV DAL_PLAYER=claude

# ── Security hardening (claude-code-container pattern) ──

# dumb-init for PID 1 signal handling + zombie reaping
RUN apt-get update -qq && apt-get install -y -qq dumb-init && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Strip ALL setuid/setgid bits — prevent privilege escalation
RUN find / -xdev -perm -4000 -type f -exec chmod u-s {} \; 2>/dev/null || true && \
    find / -xdev -perm -2000 -type f -exec chmod g-s {} \; 2>/dev/null || true

# Remove network reconnaissance tools
RUN rm -f /usr/bin/nc /usr/bin/netcat /bin/netstat /usr/bin/ss /usr/bin/nmap 2>/dev/null || true

# Prevent core dumps (API keys in memory)
ENV RLIMIT_CORE=0

# File descriptor limit
ENV RLIMIT_NOFILE=1024

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
ENTRYPOINT ["dumb-init", "--"]
CMD ["/usr/local/bin/entrypoint.sh"]
