FROM ubuntu:24.04

RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends \
      bash git curl ca-certificates nodejs npm gpg wget \
      build-essential pkg-config libssl-dev && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Rust (stable)
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable
ENV PATH="/root/.cargo/bin:${PATH}"

RUN npm install -g @anthropic-ai/claude-code

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list && \
    apt-get update -qq && apt-get install -y -qq gh && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /root/.claude/skills /root/.claude/hooks

# Git credential helper
RUN git config --global credential.helper '!f() { echo username=x-access-token; echo "password=$GH_TOKEN"; }; f'

# Quorum — multi-agent consensus & orchestration
RUN npm install -g quorum

ENV DAL_ROLE=member
ENV DAL_PLAYER=claude

COPY entrypoint.sh /usr/local/bin/entrypoint.sh

WORKDIR /root
CMD ["/usr/local/bin/entrypoint.sh"]
