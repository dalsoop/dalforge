FROM ubuntu:24.04

RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends \
      bash git curl ca-certificates nodejs npm gpg && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN npm install -g @openai/codex

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list && \
    apt-get update -qq && apt-get install -y -qq gh && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /root/.codex/skills

RUN git config --global credential.helper '!f() { echo username=x-access-token; echo "password=$GH_TOKEN"; }; f'

COPY entrypoint.sh /usr/local/bin/entrypoint.sh

# Quorum — multi-agent consensus & orchestration
RUN npm install -g quorum

ENV DAL_ROLE=member
ENV DAL_PLAYER=codex

WORKDIR /root
CMD ["/usr/local/bin/entrypoint.sh"]
