# Squirtle Squad — Product Vision

## What it is

A Claude Code plugin that packages a reusable "software engineering team" — a set of agents and skills that, installed into any repo, lets a human drive a swarm of Claude Code workers to implement Linear tickets end-to-end.

The "team" for v1 is the existing coral-benchmarks pattern, extracted:

- **ticket-manager** — an agent that triages Linear tickets and dispatches work
- **worker** — an agent that implements one ticket in an isolated git worktree and opens a PR
- Supporting skills: `linear`, `pull-request`, `assign-worker`

Squirtle Squad lifts this pattern out of coral-benchmarks, parameterizes the repo-specific bits via a TOML config, and distributes it as a Claude Code plugin so any repo can run the same loop.

## The frame

Think of it as: *Claude Code's general-purpose coding agent, but wrapped in a workflow that makes it act like a software engineering team with product management and version control built in.* Humans interact with Linear and PRs the way they already do; the squad handles the space between.

## Who it's for

**V1 audience**: Phoebe teammates. Small startup. Multiple Linear projects. Every early user is someone we can walk through setup in person, so we trade onboarding polish for speed.

**Aspirational**: Open source — any Claude Code user with a Linear workspace. This constrains v1: no Phoebe-specific defaults leak into the plugin (every org/team/project ID lives in the consumer's TOML, not the plugin). But we do not build for strangers yet.

## What success looks like (v1)

Two milestones, both required:

1. **Coral canary.** coral-benchmarks installs squirtle-squad, deletes its own `.claude/agents/` and `.claude/skills/`, and the existing ticket-manager / worker workflow behaves identically against the BENCH Linear team. Hard cutover.
2. **Self-dogfooding.** The squirtle-squad repo uses the plugin's own agents to work tickets on the squirtlesquad Linear project — close at least one ticket via a worker-opened PR against this repo.

## Customization as a principle

Squirtle Squad ships a *template* of a software-engineering workflow — ticket-manager triages, workers implement in parallel, each opens a PR. The template is opinionated so it works out of the box. Every part is meant to be customizable and extendable:

- **Scalars and IDs.** Team IDs, project UUIDs, labels, ticket prefixes, worktree paths, branch prefixes — all live in the consumer's `.agent_squad/config.toml`. Change repo, change config.
- **Prompts and skills.** Claude Code's native layering lets a consumer repo ship its own `.claude/skills/<name>/` or `.claude/agents/<name>.md` that supplements or replaces plugin-provided ones. Exact override semantics are TBD (see `open-questions.md` Q7).
- **New capabilities.** Consumer repos can add entirely new agents and skills alongside the plugin's — the squad is a starting template, not a closed system.

What v1 does *not* build: named extension slots, append/prepend semantics, or merge-based prompt overlays. If a consumer needs control beyond TOML, they fork the plugin's file into their own `.claude/` and edit it. Simple mental model, accepted drift cost in v1; revisit if it causes real pain.

## Quality & architecture principles

The squad is agents running agents, and prompts are code. Sloppy compounds fast. We hold the bar high:

- **Minimal surface area.** Every agent, skill, and config key has a reason to exist. Delete-first instinct. Shorter prompts beat longer ones unless the length buys something concrete.
- **One responsibility per component.** Agents and skills do one thing well. If a prompt grows a second job, split it.
- **Strong boundaries.** Plugin ↔ TOML ↔ consumer local are three discrete layers. The plugin ships zero IDs; consumer repos don't reach into plugin internals. A consumer that only edited TOML upgrades the plugin with no merge conflicts.
- **Explicit over clever.** Hardcoded values are fine when the alternative is a knob only one consumer ever turns. Add parameters when there's a second real caller, not before.
- **No half-finished state.** When a skill is extracted, the coral canary immediately consumes it *and* coral's local copy is deleted. No phantom fallbacks, no "temporary" duplication. Every milestone exits cleanly or we haven't finished it.
- **Canary against real behavior.** Exit criteria test coral doing its actual work — fetching a real ticket, opening a real PR. Synthetic tests are not sufficient proof.
- **Own your dependencies.** Runtime deps are Claude Code, bash, `curl`, and the Linear API. No hidden toolchain assumptions in the plugin; consumer repos stack their own tools on top.
- **Inspectability is a feature.** Resolved prompts, config, and agent hierarchies should be readable by a human in seconds. Any indirection (templating, overrides) must come with a way to see what actually ran — see the future `squad doctor` in the parking lot.

This is the bar. If a PR doesn't meet it, it doesn't land — regardless of who opened it.

## Non-goals (v1)

- No PM tool other than Linear. Jira/GitHub Issues are v2+.
- No PR-review-comment polling loop. Humans review; if they want the worker to address feedback they re-invoke manually. (Tracked as BENCH-209; becomes v1.1.)
- No reviewer subagent, no coordinator role, no cross-ticket dependency scheduling. One level of hierarchy only.
- User auth only. V1 uses each user's personal Linear API key via env var. App/bot/OAuth tokens — useful when a scheduled or remote agent needs credentials not tied to a specific human — are v1.1+.
- No remote execution (K8s, scheduled remote agents). Local Claude Code only.
- No public marketplace listing. Private GitHub repo is the distribution channel.
- No polished onboarding docs for strangers.

## Timeline & quality

Open-ended. Quality over speed. We would rather spend a week spiking the config-substitution mechanism than ship a runtime-read hack and rewrite in a month. The canary and dogfooding milestones are the forcing functions, not a calendar.

## Why this shape

Two observations drove the v1 scope:

1. **"Reusable skills" is a thinner story than it looks.** Coral's existing skills have hardcoded IDs, team names, and paths woven through the prompts. The value isn't the skill files — it's the *orchestration pattern* (ticket-manager spawns workers spawns PRs) and the pluggable surfaces that let it fit a repo. So we're productizing the pattern, not just relocating files.
2. **Two consumers is the minimum useful test.** Coral alone doesn't force us to parameterize anything honestly — we'd just move hardcoded IDs from coral to the plugin. The squirtle-squad-on-itself dogfooding forces a second, distinct Linear workspace and a second repo layout, which is the cheapest honest test of "does the config surface actually work."
