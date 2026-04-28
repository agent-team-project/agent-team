# CLAUDE.md

Contributor orientation for `agent-team`. `README.md` is user-facing; this file is for anyone working *on* the CLI.

## What it is

A Python CLI that:

1. Vendors a starter set of agent definitions and skills into a consumer's repo at `.agent_team/`.
2. Launches Claude Code with those agents and skills registered for the session, via Claude Code's `--agents` and `--add-dir` flags.

Everything is per-repo and file-based. There is no plugin install, no marketplace, no global state. The bundled starter (a software-engineering team — `ticket-manager`, `manager`, `worker`, plus `linear` / `pull-request` / `assign-worker` skills) is one template among many possible. Users are expected to edit, replace, or wholly rewrite it.

## Vocabulary

- **agent** — a definition at `.agent_team/agents/<name>/`. Authored, static.
- **instance** — a named runtime spawn of an agent (`name=` at spawn time). Has its own state at `.agent_team/state/<instance-name>/`. One agent can have many instances.
- **workspace** — an instance's working directory. For ephemeral code-writing agents: a fresh worktree per spawn (Claude Code's `Agent` tool with `isolation: "worktree"`). For others: the repo root.

## Forward-looking architecture

Two design sketches capture where the project is going. Read the relevant one if you're touching code in its area.

- [`documentation/orchestrator.md`](./documentation/orchestrator.md) — v1.1+ Go daemon (`agent-teamd`) that owns instance lifecycle, replaces Claude Code's in-session dispatch primitives with an orchestrator-mediated model, and unblocks runtime-agnostic execution. Read before touching the dispatch path or thinking about persistent / restartable instances.
- [`documentation/templates.md`](./documentation/templates.md) — v1.2+ templates-as-images model with parameter substitution and a layered config resolution chain. The `template` resource verb (SQU-22), the `init <ref>` flow, and the bundled starter's evolution into a parameterized "default template" all live here. Read before touching `init`, the `template` verb, `loader`, or `config.toml` shape.

## Repo layout

- `cli/` — the Python package.
  - `cli/src/agent_team/cli.py` — entrypoint (Typer).
  - `cli/src/agent_team/loader.py` — pure logic: parse frontmatter, load agents, resolve skills.
  - `cli/src/agent_team/commands/` — one module per top-level command (`init`, `run`, `doctor`) or resource group (`agent`, `skill`, `instance`).
  - `cli/src/agent_team/template/` — bundled starter content, copied into consumer repos by `init`.
- `cli/pyproject.toml` — Python ≥3.11. One runtime dep (`typer`); resist further deps. The future runner is a separate program (likely Go).
- `.agent_team/` (this repo) — our own team, since we self-dogfood. `agents/` and `skills/` are symlinks into the bundled template.
- `scripts/ci/` — CI validators and smoke tests.
- `.github/workflows/ci.yml` — runs the validators on push and PR.

## CLI dev loop

From repo root:

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

## How `agent-team run <agent>` works

For each `.agent_team/agents/<name>/agent.md`:
1. Split YAML frontmatter from the body. The launcher uses a stdlib-only mini-parser that handles scalar and block-scalar values (no PyYAML at runtime).
2. `description` from frontmatter becomes the agent's description; body becomes the agent's prompt.
3. Directory name becomes the agent's name (e.g. `agents/worker/` → subagent `worker`).
4. Skills are resolved: every `<agent>/skills/<name>/SKILL.md` is auto-included; `[skills].extra = ["..."]` in `<agent>/config.toml` pulls in shared skills (looked up under `.agent_team/skills/<name>/`) or arbitrary paths.

The CLI assembles `{name: {description, prompt}, …}` as JSON, builds a tmpdir with `.claude/skills/<name>` symlinks for the union of all referenced skills, writes the chosen agent's prompt + a kickoff preamble (instance name, state dir) to a temp file, creates `.agent_team/state/<instance>/` if missing, and exec's:

```sh
claude --agents '<json>' --add-dir <tmpdir> --append-system-prompt-file <kickoff> <forwarded-args>
```

The launched session IS the named agent (its prompt is the system prompt) AND has every other agent registered as a subagent (so e.g. a spawned `manager` can dispatch a `worker` via the Task tool).

The launcher exports into claude's env:
- `AGENT_TEAM_ROOT` — absolute path to `.agent_team/`
- `AGENT_TEAM_INSTANCE` — the instance name
- `AGENT_TEAM_STATE_DIR` — absolute path to `.agent_team/state/<instance>/`

Skills are picked up by Claude Code's `--add-dir` discovery — see [Skills docs](https://code.claude.com/docs/en/skills) for the directory shape `--add-dir` expects.

`.agent_team/config.toml` is read by skill bash via `python3 -c 'import tomllib; …'`. The CLI does not substitute prompt templates — values flow through the filesystem at runtime.

## Self-dogfooding

This repo's `.agent_team/agents` and `.agent_team/skills` are symlinks into `cli/src/agent_team/template/`, so edits to template content are immediately live for the next `agent-team run`. If you've broken the wiring, recreate the symlinks by hand or wipe `.agent_team/{agents,skills}` and re-link.

## Contribution rules

### Branches

One branch per ticket, prefixed meaningfully (e.g. `squ-17-claude-md`). When the bundled `worker` agent runs in a worktree, it follows the same convention.

### Tickets

Tickets for this repo use the `SQU` prefix and live in the `squirtlesquad` Linear workspace. Routing is handled by `ticket-manager` reading `.agent_team/config.toml`.

### Commits

Match the existing history (`git log --oneline`). Conventions:

- Tag with a category or milestone: `docs: …`, `fix(cli): …`, `chore: …`, or a milestone tag if one applies.
- Include the ticket identifier when the commit closes or substantially advances one.
- Trailer: `Co-authored-by: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` on any commit an agent helped author.

### PR body

Lead with a short summary of what changed and why. Link the ticket via `Closes https://linear.app/squirtlesquad/issue/SQU-<n>/<slug>`. End with the standard Claude Code footer.

### Quality bar

- Minimal surface area. One responsibility per component.
- No half-finished code paths. No dead code, no commented-out blocks.
- Strong layer boundaries: CLI ↔ template ↔ vendored copy ↔ consumer extensions.
- If a value would be hardcoded in a template file (UUID, label, path, ticket prefix), it goes in `.agent_team/config.toml` instead. Extend the schema rather than embedding.
- Runtime CLI deps stay minimal — currently `typer` only.

If a PR doesn't meet this bar, it doesn't land.

Keep this file short. When it grows past ~150 lines, prune.
