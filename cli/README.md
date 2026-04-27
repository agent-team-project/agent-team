# agent-team CLI

The `agent-team` Python package — distributes the team as a CLI.

This directory is package internals. User-facing docs live in the [repo root README](../README.md) and [`documentation/`](../documentation/).

## Layout

```
cli/
├── pyproject.toml
└── src/agent_team/
    ├── cli.py                 # argparse entrypoint (`agent-team`)
    ├── commands/              # init, sync, add, doctor
    └── template/              # bundled — copied into <consumer>/.agent_team/ on init
        ├── agents/            # ticket-manager.md, worker.md, manager.md
        ├── skills/            # linear, pull-request, assign-worker
        ├── scripts/           # linear-graphql.sh
        ├── managers/          # convention dir for per-manager scopes
        └── config.toml.example
```

## Local dev

From this directory:

```sh
uv run --with-editable . agent-team --help
```

Or install editably and run:

```sh
uv pip install -e .
agent-team --help
```
