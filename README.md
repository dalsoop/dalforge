<div align="center">
  <h1>dalcenter</h1>
  <p><strong>Dal lifecycle manager — wake, sleep, sync AI agent containers</strong></p>
  <p>
    <a href="https://github.com/dalsoop/dalcenter"><img src="https://img.shields.io/badge/github-dalsoop%2Fdalcenter-181717?logo=github&logoColor=white" alt="GitHub repository"></a>
    <a href="./LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-2563eb.svg" alt="AGPL-3.0 License"></a>
  </p>
  <p><a href="./README.ko.md">한국어</a></p>
</div>

dalcenter manages dal (AI puppets) — Docker containers with Claude Code, Codex, or Gemini installed, each with their own skills, instructions, and git identity. Templates live in git (localdal), dalcenter handles the runtime.

## Quick Start

```bash
# 1. Start the daemon
dalcenter serve --addr :11190 --repo /path/to/your-project \
  --mm-url http://mattermost:8065 --mm-token TOKEN --mm-team myteam

# 2. Initialize localdal in your project
dalcenter init --repo /path/to/your-project

# 3. Create dal templates (via git)
# .dal/leader/dal.cue + instructions.md
# .dal/dev/dal.cue + instructions.md
# .dal/skills/code-review/SKILL.md

# 4. Validate
dalcenter validate

# 5. Wake dals
dalcenter wake leader
dalcenter wake dev
dalcenter ps

# 6. Sleep when done
dalcenter sleep --all
```

## How It Works

```
.dal/ (git-managed, localdal)
  leader/dal.cue + instructions.md     ← dal template
  dev/dal.cue + instructions.md
  skills/code-review/SKILL.md          ← shared skills

dalcenter serve
  → starts HTTP API
  → starts repo-watcher (git fetch/pull every 2min)
  → starts credential-watcher (token refresh)

dalcenter wake dev
  → reads .dal/dev/dal.cue
  → creates Docker container (dalcenter/claude:latest)
  → injects instructions.md → CLAUDE.md
  → mounts skills, credentials, service repo
  → injects dalcli binary
  → dal starts working

git push (GitHub)
  → repo-watcher detects remote changes (within 2min)
  → git pull --ff-only
  → if .dal/ changed → auto sync to running containers
```

## Architecture

```
LXC: dalcenter
├── dalcenter serve          HTTP API + Docker management
│   ├── repo-watcher         git fetch/pull → auto sync
│   └── cred-watcher         token expiry → auto refresh
├── Docker: leader (claude)  dalcli-leader inside
├── Docker: dev (claude)     dalcli inside
└── Docker: dev-2 (claude)   multiple instances supported
```

## CLI

```
dalcenter serve                   # daemon (HTTP API + repo-watcher + Docker)
dalcenter init --repo <path>      # initialize localdal (.dal/ + subtree)
dalcenter wake <dal> [--all]      # create Docker container
dalcenter sleep <dal> [--all]     # stop Docker container
dalcenter sync                    # propagate changes to running containers
dalcenter validate [path]         # CUE schema + reference validation
dalcenter status [dal]            # show dal status
dalcenter ps                      # list awake dals
dalcenter logs <dal>              # container logs
dalcenter attach <dal>            # enter container
```

### Inside containers

```
dalcli-leader (leader only)       dalcli (all members)
  wake <dal>                        status
  sleep <dal>                       ps
  ps                                report <message>
  status <dal>
  logs <dal>
  sync
  assign <dal> <task>
```

## dal.cue

```cue
uuid:    "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
name:    "dev"
version: "1.0.0"
player:  "claude"
role:    "member"
channel_only: true
skills:  ["skills/code-review", "skills/testing"]
hooks:   []
git: {
    user:         "dal-dev"
    email:        "dal-dev@myproject.dev"
    github_token: "env:GITHUB_TOKEN"
}
```

## localdal Structure

```
.dal/
  dal.spec.cue              schema definition
  leader/
    dal.cue                 uuid, player, role:leader
    instructions.md         → CLAUDE.md at wake
  dev/
    dal.cue                 uuid, player, role:member
    instructions.md
  skills/
    code-review/SKILL.md    shared across dals
    testing/SKILL.md
```

## Communication

Dals communicate via Mattermost. One channel per project (auto-created on serve).

- `dalcli-leader assign dev "task"` → posts `@dal-dev 작업 지시: task`
- `dalcli report "done"` → posts `[dev] 보고: done`

## File Name Conversion

| Source | Player | In Container |
|---|---|---|
| instructions.md | claude | CLAUDE.md |
| instructions.md | codex | AGENTS.md |
| instructions.md | gemini | GEMINI.md |

## Credentials

dalcenter auto-mounts player credentials into containers (read-only bind mount). Expired tokens trigger a warning at wake time.

| Player | Host Path | Container Path | Expiry Check |
|--------|-----------|---------------|-------------|
| claude | `~/.claude/.credentials.json` | `~/.claude/.credentials.json` | `expiresAt` (ms) |
| codex | `~/.codex/auth.json` | `~/.codex/auth.json` | `tokens.expires_at` (RFC3339) |
| gemini | env `GEMINI_API_KEY` | env `GEMINI_API_KEY` | — |

### Proxmox LXC Setup

When dalcenter runs inside an LXC container, credentials must be synced from the Proxmox host:

```bash
pve-sync-creds [CT_ID]   # default: 105
```

Uses `tee` (in-place write) to preserve file inode — Docker bind mounts stay intact.

### Token Refresh

- **Claude**: OAuth token. If expired, run `claude auth login` on the host, then `pve-sync-creds`.
- **Codex**: ChatGPT OAuth. If expired, run `codex auth login` on the host, then `pve-sync-creds`.
- **Gemini**: API key (no expiry). Set `GEMINI_API_KEY` env var on the dalcenter host.
- When a running dal hits an auth failure, dalcli now auto-files a host-action claim and posts a short credential-sync notice instead of only emitting a plain chat message.
- If `DALCENTER_CRED_OPS_ENABLED` is on (default), dalcenter also tries to run the documented credential sync flow and reports start/success/failure to the `dal-ops` channel.
- If dalcenter runs inside an LXC and cannot execute `proxmox-host-setup` or `pve-sync-creds` directly, configure a host bridge with `DALCENTER_CRED_OPS_HTTP_URL` and `DALCENTER_CRED_OPS_HTTP_TOKEN`.
- A reference bridge server is included at [`scripts/dalcenter-cred-ops-httpd.py`](./scripts/dalcenter-cred-ops-httpd.py). It exposes `POST /sync` and runs `proxmox-host-setup ai sync --agent <player>` followed by `pve-sync-creds <vmid>` on the Proxmox host.

### Channel-only teams

Add `channel_only: true` to a dal profile when the team should never poll Mattermost DMs.
The dal will only react in the project channel and its active threads.

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md).
