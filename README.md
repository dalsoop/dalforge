<div align="center">
  <h1>dalcenter</h1>
  <p><strong>Dal lifecycle manager — join, provision, reconcile AI agent instances</strong></p>
  <p>
    <a href="https://github.com/dalsoop/dalcenter"><img src="https://img.shields.io/badge/github-dalsoop%2Fdalcenter-181717?logo=github&logoColor=white" alt="GitHub repository"></a>
    <a href="./LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-2563eb.svg" alt="AGPL-3.0 License"></a>
  </p>
  <p><a href="./README.ko.md">한국어</a></p>
</div>

dalcenter manages dal (AI agents) — instances provisioned from `.dalfactory` manifests with skills, secrets, and health checks. Packages live in dalforge cloud or local sources, dalcenter handles the lifecycle.

## Quick Start

```bash
# 1. Start the API server
dalcenter serve --port 10100

# 2. Browse and join a dal package
dalcenter catalog search agent-coach
dalcenter join dalcli-agent-coach

# 3. Validate manifests
dalcenter validate

# 4. Provision and start
dalcenter provision <name>
dalcenter start <name>
dalcenter list

# 5. Stop when done
dalcenter stop <name>
```

## How It Works

```
.dalfactory/ (manifest)
  dal.cue                            ← dal definition + templates

dalcenter join <source>
  → downloads/clones package source
  → creates instance in ~/.dalcenter/instances/

dalcenter provision <name>
  → reads .dalfactory/dal.cue
  → provisions LXC container (bridge, cores, memory, storage)

dalcenter start <name>
  → runs build.entry from manifest
  → exports skills to Claude/Codex
  → health check begins

dalcenter reconcile
  → checks all instances
  → repairs exports (skills, hooks, settings)
```

## Architecture

```
LXC: dalcenter
├── dalcenter serve          API server (port 10100)
├── dalcenter watch          continuous reconcile loop
├── instance: leader         dalcli-leader inside
├── instance: dev            dalcli inside
└── instance: dev-2          multiple instances supported
```

## CLI

```
dalcenter serve                          # API server (dal registry)
dalcenter join <source>                  # create instance from .dalfactory manifest
dalcenter provision <name>               # provision LXC container
dalcenter start <name>                   # start process in instance
dalcenter stop <name>                    # stop process
dalcenter restart <name>                 # restart process
dalcenter destroy <name>                 # stop and destroy container
dalcenter list                           # list all instances
dalcenter status [name]                  # show instance status
dalcenter validate [path...]             # CUE schema validation
dalcenter reconcile                      # check and repair all exports
dalcenter watch                          # continuous reconcile (default 60s)
dalcenter export [path...]               # export Claude skills from manifests
dalcenter unexport [path...]             # remove exported skills
dalcenter secret set <name>              # store secret (reads from stdin)
dalcenter secret get <name>              # retrieve secret
dalcenter secret list                    # list all secrets
dalcenter catalog search [query]         # browse dalforge cloud packages
dalcenter talk run                       # dal communication daemon (MM bridge)
dalcenter talk conductor                 # central orchestrator bot
dalcenter talk setup                     # create Mattermost bot account
dalcenter talk teardown                  # disable bot account
dalcenter tui                            # interactive terminal dashboard
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

## .dalfactory/dal.cue

```cue
schema_version: "1.0.0"

dal: {
    id:       "DAL:CLI:848a4292"
    name:     "dalcli-agent-coach"
    version:  "0.1.0"
    category: "CLI"
}

description: "AI agent coaching CLI tool"

templates: default: {
    schema_version: "1.0.0"
    name:           "default"
    description:    "Default runtime"
    container: {
        base:     "ubuntu:24.04"
        packages: ["bash", "python3"]
        agents: {}
    }
    permissions: {
        filesystem: ["/tmp/dal-*"]
        network:    false
    }
    build: {
        language: "python"
        entry:    "bin/app"
        output:   "bin/app"
    }
    health_check: {
        command: "bin/app status"
    }
    exports: claude: {
        skills: ["skills/my-skill/SKILL.md"]
    }
}
```

## Communication

Dals communicate via Mattermost. `dalcenter talk` manages bot accounts and message routing.

```bash
# Set up a bot for a dal
dalcenter talk setup --url http://mm:8065 --username dal-dev \
  --login admin@example.com --password pass --channel project

# Run communication daemon
dalcenter talk run --url http://mm:8065 --bot-token TOKEN \
  --bot-username dal-dev --channel-id CHID

# Run central orchestrator
dalcenter talk conductor --url http://mm:8065 --bot-token TOKEN \
  --channel-id CHID --dal dev:member --dal leader:leader
```

- `dalcli-leader assign dev "task"` → posts `@dal-dev task`
- `dalcli report "done"` → posts `[dev] done`

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md).
