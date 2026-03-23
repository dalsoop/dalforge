<div align="center">
  <h1>dalcenter</h1>
  <p><strong>Self-hosted orchestration for turning <code>.dal-template</code> declarations into local runtime and Proxmox LXC operations.</strong></p>
  <p>
    <a href="https://github.com/dalsoop/dalcenter"><img src="https://img.shields.io/badge/github-dalsoop%2Fdalcenter-181717?logo=github&logoColor=white" alt="GitHub repository"></a>
    <a href="https://github.com/dalsoop/dalcenter/releases"><img src="https://img.shields.io/github/v/release/dalsoop/dalcenter?display_name=tag" alt="GitHub release"></a>
    <a href="https://dalcenter.com"><img src="https://img.shields.io/badge/home-dalcenter.com-0f766e?logo=googlechrome&logoColor=white" alt="Website"></a>
    <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-2563eb.svg" alt="MIT License"></a>
  </p>
  <p><a href="./README.ko.md">한국어</a></p>
</div>

`dalcenter` is a self-hosted orchestration stack that turns `.dal-template` declarations into local runtime and LXC operations. `dalcenter` distributes packages and specs, reads `.dal-template` from user repos, registers, and manages them.

Canonical domain: `https://dalcenter.com`

## What It Is

In short:

- **`dalcenter`** — Self-hosted hub that distributes packages and specs, reads `.dal-template`, handles registration, export, execution, and provisioning
- **User repo** — The actual project containing a `.dal-template/` directory

Or in one line:

`.dal-template` is the SSOT. `dalcenter` distributes, executes, and manages.

## Why It Exists

The problem is usually this:

- Skills, hooks, and runtime configs are scattered across repos
- Local execution and container execution easily drift apart
- Reproducing which repo requires which agent environment is difficult

`dalcenter` bundles all of this into a single `.dal-template` declaration, connecting local exports all the way to Proxmox LXC provisioning.

## Current Capabilities

`dalcenter` currently connects `.dal-template` to real operational workflows:

- `catalog search` for querying dalcenter cloud packages
- `.dal-template` validate
- `join/list/status` for repo registration and inspection
- Claude/Codex skill export, Claude hook export and settings injection
- `start/stop/restart` for local process management
- `reconcile/watch` for drift detection
- Proxmox LXC `provision/destroy`
- `container.packages` installation
- `container.agents` install command execution
- `dal summon/dismiss` with soft export/unexport/destroy integration

Live Proxmox verification is complete:

- Ubuntu 24.04 LXC creation
- `bash`, `python3`, `tmux` package installation confirmed
- `destroy` followed by container removal confirmed

This is not a design document — it is an early operational version where `.dal-template` in a user repo drives local execution and LXC operations end to end.

## Quick Start

The fastest way to start is to search for a package, register a repo, and check its status.

```bash
dalcenter catalog search agent-coach
dalcenter join agent-coach
dalcenter list
dalcenter status agent-coach
```

## What Success Looks Like

A successful run looks like this:

```text
NAME                BRANCH  DESCRIPTION
dalcli-agent-coach  main    Tmux pane monitoring, stagnant detection, and LLM coachin...
```

```text
staged package: dalcli-agent-coach -> ~/.dalcenter/sources/dalcli-agent-coach
instance created: dal-... (template=default, source=cloud:dalcli-agent-coach, skills=2)
health check: ok
```

```text
DAL_ID                SOURCE       TEMPLATE  STATUS  HEALTH      SKILLS  CREATED
dal-...               agent-coach  default   ready   ok(0s ago)  2       ...
```

```text
source_type:    cloud
source_ref:     dalcli-agent-coach
health_status:  ok
```

## Docs

- [`docs/README.md`](./docs/README.md) — Documentation index
- [`docs/runbooks/first-join-and-provision.md`](./docs/runbooks/first-join-and-provision.md) — Shortest runbook: register a package and spin up an LXC

## Architecture

```
dalforge/
  dalcenter/                     Central registry + secret management
    dal.spec.cue                 Core spec (CUE)
  dalcli/                        CLI tool packages
    dalcli-agent-coach           Agent pane monitoring + coaching
    dalcli-custom-functions      Function registry + command history
    dalcli-task-queue            Task queue + sequential execution
    dalcli-lxc-stage-player      LXC stage execution entry point
    dalcli-agent-tool-syncer     Doc SSOT sync + link watcher
    dalcli-agent-bridge          Inter-agent relay
```

## Core Concepts

### dal (puppet)

A dal is an AI agent instance. Inside its container, agents like Claude, Codex, or Gemini are already installed and authenticated. One dal equals one working environment.

### dalcenter (hub + management)

`dalcenter` is the self-hosted hub that distributes packages and specs, reads `.dal-template`, registers packages, and manages runtime state.

- Package distribution, spec and documentation delivery, version catalog
- Package (CLI/skill/hook) registration and versioning
- Instance creation and state tracking
- Secret (API keys, etc.) encrypted storage and distribution
- Per-node installation inventory
- Audit event logging

### .dal-template (repo declaration)

A directory at the root of a user repo. It lives in the actual project repo, not in the dalcenter hub repo. This directory is the SSOT for execution, exports, containers, and agents.

```
my-project/
  .dal-template/
    dal.cue                      Dal definition for this repo
    templates/
      claude-dev.cue             Claude development puppet template
      claude-review.cue          Claude review puppet template
      codex-worker.cue           Codex worker puppet template
      full-stack.cue             Full agent puppet template
  src/
  ...
```

### PLAYER (agent)

The entity that actually works inside an execution environment. A single dal can have multiple PLAYERs. Each PLAYER can be a different agent (Claude, Codex, Gemini) with a different tool set.

## ID System

Every dal component has a unique ID. Even if the name changes, the ID stays permanent.

```
DAL:{CATEGORY}:{uuid8}

DAL:CLI:3a8c1f02          dalcli-agent-coach
DAL:CLI:7e4b9d15          dalcli-custom-functions
DAL:PLAYER:f1d24e83       claude-dev player
DAL:CONTAINER:a1b2c3d4    my-project container
```

### Categories

| Category | Description |
|---|---|
| CLI | Command-line tool |
| PLAYER | Execution environment (agent) |
| CONTAINER | Container service |
| SKILL | Agent skill |
| HOOK | Event hook |

Categories are extensible. Adding a new category requires no change to dal.spec.cue — just register it in dalcenter.

## Workflow

### 1. Define a template

Define a puppet template in `.dal-template/templates/claude-dev.cue`. Declare the container base image, packages to install, agents, CLI tools, skills, and required secrets.

### 2. Register a repo

```bash
dalcenter join /path/to/repo
```

`join` currently performs:

1. Read `.dal-template/dal.cue` from the user repo
2. Validate the manifest
3. Export skills and hooks
4. Create a local instance directory
5. Write registry and state

```bash
dalcenter list
dalcenter status <name-or-id>
```

### 3. Build and export

`.dal-template/` is the source (SSOT). Agent-specific configs are exported as build artifacts.

The primary rule is to export original assets directly from the repo root (e.g., `skills/{name}/SKILL.md`). Hub-style repos may optionally declare `source/document/skills/{name}/SKILL.md` as a fallback export source.

```
.dal-template/ (source)
    -> export
.claude/
    skills/
    hooks/
    settings.json
.codex/
    skills/
```

Hook settings injection currently supports Claude only. Codex supports skill export only.

### 4. Secret management

Sensitive data such as API keys is stored encrypted (AES-256-GCM) in the built-in SecretVault. When an agent runs inside a container, secrets are automatically decrypted and injected.

```bash
dalcenter secret set anthropic_api_key
dalcenter secret set openai_api_key
dalcenter secret list
```

### 5. Synchronization

`dalcenter` periodically syncs registered repos and runtime state.

- Detect package version updates
- Report installation status
- Operate in cache mode when offline

Key commands:

```bash
dalcenter reconcile
dalcenter watch --interval 60
```

## Usage Examples

### Validate a manifest

```bash
dalcenter validate /root/dalforge/dalcli/dalcli-agent-coach
```

### Search dalcenter packages

```bash
dalcenter catalog search agent-coach
```

### Register a repo

```bash
dalcenter join /root/dalforge/dalcli/dalcli-agent-coach
dalcenter list
dalcenter status dalcli-agent-coach
```

Register by package name (fetches from dalcenter):

```bash
dalcenter join agent-coach
```

### Manage local execution

If `build.entry` is defined, runs without `--command`:

```bash
dalcenter start dalcli-agent-coach
dalcenter status dalcli-agent-coach
dalcenter stop dalcli-agent-coach
```

Minimum success criteria:

- `list` shows `STATUS=ready`
- `HEALTH=ok(...)`
- `status` shows `source_type`, `source_ref`, `health_status`

### Provision a Proxmox LXC

```bash
dalcenter provision dalcli-agent-coach \
  --vmid 219318 \
  --storage local-lvm \
  --bridge vmbr0 \
  --memory 512 \
  --cores 1
```

Supported flags: `--vmid`, `--storage`, `--bridge`, `--memory`, `--cores`, `--dry-run`

### Destroy a Proxmox LXC

```bash
dalcenter destroy dalcli-agent-coach
```

### Integrate with dal

```bash
dal summon agent-coach
dal dismiss agent-coach
```

`dal summon` calls export as a soft dependency. `dal dismiss` calls unexport and destroy as soft dependencies.

## Current Limitations

Usable today, but these areas still have room to grow:

- Advanced network and storage policies
- Additional operational flags (e.g., disk size)
- Hook operation example manifests
- Proxmox-scale audit and policy refinement

## What It Is Not

- A tool that only installs packages
- A product with only a cloud SaaS control plane
- A runtime that assumes manual configuration without `.dal-template`
- A finished large-scale multi-tenant operations platform

## Tradeoffs

- More structure and concepts than a simple script
- Operational complexity rises when covering Proxmox/LXC
- `.dal-template` declarations must be maintained, but in return you get reproducibility and manageability

## Rough Comparison

| Tool type | Baseline model | How dalcenter differs |
|---|---|---|
| Simple CLI installer | Install packages and done | dalcenter reads `.dal-template`, handles registration, export, state, and provisioning |
| General task runner | Local command execution | dalcenter manages repo declarations and runtime state together |
| Container provisioning tool | Infrastructure creation | dalcenter also covers skill/hook export and local execution flows |

## Current Gaps

Compared to more productized agent stacks like OpenClaw, dalcenter is still weaker in:

- No unified agent-facing gateway
- No session-level context compaction policy
- `.dal-template`-based skill/hook discovery works, but auto-registration UX is still rough
- Per-service health exposure and healthcheck contracts are not fully unified

In other words: it is a working operational stack, but the first impression is still closer to "a well-built self-hosted toolkit."

## Near-Term Priorities

1. Add a shared agent gateway layer
2. Strengthen skill/hook auto-discovery on `join/export`
3. Unify `/healthz` and container healthcheck contracts
4. Add session compaction policy instead of manual split/reset

## Spec

All contracts are defined in CUE at `dalcenter/dal.spec.cue`. This file is the foundation of dalforge, and all tools follow this spec.

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) to get started.
