# Open Questions

Research tasks and decisions not yet committed. Each has an owner, a blocker relationship, and a "next action" so it's clear how to close it.

---

## Q1 — Plugin manifest & marketplace schema

**Status**: unverified against current Claude Code docs.
**Blocks**: M1 (skeleton).

Training data may be stale on Claude Code plugin schemas. Need concrete answers to:

- Required and optional fields in `.claude-plugin/plugin.json`.
- How agents and skills are registered inside a plugin (is it convention-based `agents/` + `skills/` directories, or explicit manifest entries?).
- `.claude-plugin/marketplace.json` format for a self-hosted marketplace.
- How `/plugin marketplace add <github-slug>` resolves a repo URL — does it read `marketplace.json` at the root, in `.claude-plugin/`, or somewhere else?
- Whether hooks (e.g. SessionStart) can ship inside a plugin or must live in consumer `.claude/settings.json`.
- Plugin versioning — git tags, SHAs, semver? How do consumers pin?

**Next action**: fetch current plugin docs from Anthropic's Claude Code reference. Likely candidate: the claude-code-guide agent plus web search against docs.claude.com / anthropic.com/engineering.

---

## Q2 — Config substitution mechanism

**Status**: three candidates, none tested.
**Blocks**: M3 (linear skill needs parameterized IDs).

| Option | Pros | Cons | Unknowns |
|---|---|---|---|
| (a) SessionStart hook renders templates | Transparent to user; no runtime token cost; user edits TOML and next session picks it up | Depends on hook reliability and timing; hook must run before agents load | Can plugin-shipped hooks write to `~/.claude/plugins/.../resolved/`? Do SessionStart hooks fire before agent prompts are cached? Can they fail gracefully? |
| (b) `squirtle-squad configure` CLI | Predictable; no hook magic; easy to debug (user can read the generated files) | User must re-run on every TOML change; one more tool to distribute and version | How does the CLI get installed alongside the plugin — same install or separate? |
| (c) Runtime LLM read of TOML | Zero machinery; agents just read the file each invocation | Token overhead per invocation; depends on LLM following instructions consistently across 10+ config keys | Token cost on a realistic coral-sized config? Instruction-following reliability for nested keys? |

**Recommended spike**:
1. Write a trivial SessionStart hook that writes a timestamp to a file; verify it fires and agents can read the file.
2. Estimate (c)'s token cost by measuring a sample TOML rendering.
3. Sketch (b)'s CLI surface.

**Decision criteria**: pick (a) if the hook fires reliably and can write files. Fall back to (b) if (a) is flaky. Reserve (c) as an escape hatch if both break.

**Next action**: spike during M0.

---

## Q3 — Local dev install loop

**Status**: unverified.
**Blocks**: M6 (dogfooding) but also nice to have for M1 so we don't need a throwaway repo.

Candidates for using the plugin from its own source tree:

- Symlink `~/.claude/plugins/<marketplace>/squirtle-squad` → this working tree.
- `/plugin install` against a `file://` or local git URL if Claude Code supports it.
- Env-var toggle (`CLAUDE_PLUGIN_DEV_MODE` or similar) that points at a local path.

**Next action**: test during M1 once we have a skeleton to install.

---

## Q4 — Naming

**Status**: undecided; low priority.

"squirtle-squad" is the internal codename. If the plugin ever goes public, we may want a name that is (a) not a Pokémon trademark reference, and (b) descriptive enough to be searchable.

**Next action**: revisit when open-sourcing is on the roadmap. Cheap to rename today (private repo, one commit), cheap next year, expensive once external consumers depend on it.

---

## Q5 — Plugin versioning & upgrade model

**Status**: partially covered by Q1.

When the plugin ships a change, how do consumers control when they pick it up?

- Does the marketplace manifest support version constraints?
- Is `/plugin update` atomic or does the user have to re-run `/plugin install`?
- What happens if we ship a breaking change to an agent prompt and coral was depending on the old behavior?

For v1 this is mostly theoretical (coral is the only consumer, we control both sides). But if squirtle-squad-on-itself and coral pin different versions of the same plugin, we need a story.

**Next action**: answer as part of Q1 research.

---

## Q6 — Credentials handling & auth model evolution

**Status**: v1 direction clear; v1.1+ direction open.

**V1 plan.** Plugin documents which env vars it reads (`LINEAR_USER_API_KEY`, `GITHUB_TOKEN`, etc.). Consumers set them however they want — `.env`, shell, keychain. No secrets in TOML. Every user uses their personal Linear API key.

**V1.1+ open question.** User API keys are fine for interactive local use but break down for:

- Scheduled agents that run without a user session.
- Shared "bot" accounts that should take actions as the squad rather than as a specific teammate.
- CI or remote execution where pinning credentials to one user is awkward.
- Attribution: a worker running under user X's key shows up in Linear as X commenting, not as "the squad." Sometimes that's desirable, sometimes not.

Directions to explore: Linear OAuth apps, Linear admin API tokens, per-repo service accounts. Each has tradeoffs around scope, rotation, and user attribution.

**Next action**: V1 confirms env-var convention during M3. V1.1+ auth model is a discrete project later.

---

## Q7 — Plugin vs. consumer-local resolution semantics

**Status**: unverified.
**Blocks**: nothing directly; informs the customization model in `architecture.md`.

When a consumer repo has `.claude/skills/foo/SKILL.md` locally AND the plugin also provides `skills/foo/`, what does Claude Code actually do?

- Does the local file fully replace the plugin's?
- Does the agent see both and choose?
- Is there a defined precedence order?

This determines how we describe "customization" to consumers. If local files cleanly supersede plugin content, we have a simple and honest escape hatch. If they coexist or conflict, we need to document a precedence rule or avoid name collisions by convention.

**Next action**: test during M1 with a deliberate name collision (trivial plugin skill + identically-named consumer local skill; see which runs).
