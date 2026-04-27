# agent-team

A CLI for declaring and launching a custom set of Claude Code subagents and skills. Each **agent** is a directory under `.agent_team/agents/`. Run `agent-team run` and the CLI launches Claude Code with your team registered for that session.

A starter "software engineering team" template (a `ticket-manager`, a `manager`, ephemeral `worker`s, plus Linear / PR / assign-worker skills) is bundled as one example. Use it as-is, edit it, or throw it away and write your own.

**Status**: pre-v1. Public API is unstable.

## Vocabulary

- **agent** — a definition. A directory at `.agent_team/agents/<name>/` containing `agent.md` (frontmatter + prompt) and `config.toml` (skill assignment). Authored, static, reusable.
- **instance** — a named runtime spawn of an agent. Identified by the `name=` parameter at spawn time. One agent can have many instances; each instance has its own state.
- **workspace** — the working directory an instance operates in. For code-writing agents (the bundled `worker`): a fresh git worktree per spawn. For others: the repo root.
- **state** — persistent per-instance files (journal, goals, progress) at `.agent_team/state/<instance-name>/`. Survives across sessions for long-lived instances; ephemeral instances (workers) keep their state inside their worktree.

## Install

```sh
uvx --from "git+https://github.com/jamesaud/agent-team#subdirectory=cli" agent-team init
```

`init` writes a starter `.agent_team/` into the current repo:

```
.agent_team/
├── config.toml                              # consumer-specific runtime values (team IDs, etc.)
├── agents/
│   ├── <name>/
│   │   ├── agent.md                         # frontmatter + prompt body
│   │   ├── config.toml                      # [skills].extra: which skills this agent uses
│   │   └── skills/                          # optional agent-private skills
│   └── ...
├── skills/
│   ├── <name>/SKILL.md                      # shared skills (referenced by any agent)
│   └── ...
└── state/                                   # per-instance state, written at runtime
    └── <instance-name>/                     # journal.md, goals.md, etc. (created on first spawn)
```

Edit anything you like, then:

```sh
agent-team run
```

…and you're in a Claude Code session with your agents and skills loaded.

## Commands

| Command | Purpose |
|---|---|
| `agent-team init [--template default\|empty]` | Vendor a starter `.agent_team/` into the current repo. |
| `agent-team add agent <name>` | Scaffold a new agent definition. |
| `agent-team add skill <name> [--agent <a>]` | Scaffold a new skill (shared by default; `--agent` scopes it under one agent). |
| `agent-team run [-- claude-args…]` | Launch a Claude Code session with all agents registered as subagents. You operate as the orchestrator. |
| `agent-team spawn <agent> [--name <instance>] [-p "<kickoff>"]` | Launch a Claude Code session that *is* one named agent. Creates `.agent_team/state/<instance>/` if missing, prepends a kickoff naming the instance and its state dir. All other agents are still registered as subagents (so a spawned `manager` can dispatch a `worker`). |
| `agent-team doctor` | Sanity-check the team layout and config. |

## How it works

`agent-team run` reads each `.agent_team/agents/<name>/agent.md`, parses the YAML frontmatter (`description`) and body (the prompt), resolves each agent's skill set from `agents/<name>/skills/` plus `[skills].extra` in `agents/<name>/config.toml`, builds a tmpdir of symlinks satisfying Claude Code's `--add-dir` skill discovery, and exec's:

```sh
claude --agents '<json>' --add-dir <tmpdir> <forwarded-args>
```

The launcher exports `AGENT_TEAM_ROOT=<absolute path to .agent_team/>` so skills can locate their bundled assets regardless of the current working directory.

Subagents are session-scoped — they exist only for the duration of the spawned `claude` process. Nothing is written into `.claude/agents/`. No plugin install, no marketplace, no global state.

## The bundled starter

`agent-team init` (default template) drops in a software-engineering team:

- **`ticket-manager`** — searches, creates, routes, and transitions Linear tickets.
- **`manager`** — persistent agent. Tracks goals and dispatches workers. State lives at `.agent_team/state/<instance-name>/`. Multiple instances of the manager agent can run side-by-side (e.g. `name=manager-billing`, `name=manager-release`), each with their own state directory.
- **`worker`** — ephemeral. One instance per ticket, each in a fresh git worktree, each delivers a PR. No persistent state — the worktree is the workspace.
- **Skills**: `linear` (GraphQL wrapper), `pull-request` (gh CLI wrapper), `assign-worker` (worker-spawn mechanics, agent-private to the manager).

`agent-team init --template empty` skips the bundled content and gives you just the directory scaffold + a stub `config.toml`.

## Working on agent-team itself

This repo dogfoods itself — its own `.agent_team/agents` and `.agent_team/skills` are symlinks into the bundled template at `cli/src/agent_team/template/`, so edits to template content are immediately live for the next `agent-team run`.

CLI dev loop:

```sh
cd cli
uv run --with-editable . agent-team --help
```

Or install editably:

```sh
cd cli && uv pip install -e .
agent-team --help
```

Smoke-test against a tmp dir:

```sh
agent-team init --target /tmp/team-smoke
```

Contributor orientation: [`CLAUDE.md`](./CLAUDE.md).
