# Security model (design sketch)

How a fleet of autonomous agents runs without any single confused agent being able to hurt you. Companion to `resource-constraints.md` (budgets bound spend; this bounds *capability*). Status: design + first slices (SQU-119 epic).

## Threat model — confusion over malice

The agents are cooperative; the risks are mis-scoping, bugs, and manipulation:

1. **Prompt injection via public input.** The repo is public: issues, PRs, and discussions are untrusted text that triage and comms agents will read. A crafted issue can instruct an agent that reads it. This is the top risk as of the open-sourcing.
2. **Secret exposure.** Agents inherit environment and filesystem access; `.env` is readable by any process as the user; child logs capture whatever agents print.
3. **Blast radius.** An agent process is the full user account. Worktree isolation is convention — nothing stops `cd /` — and we currently *disable* the runtimes' own sandboxes (`--dangerously-bypass-approvals-and-sandbox`) for daemon workers.
4. **Authority creep.** API-level verb authority exists in audit mode (SQU-92); nothing OS-level backs it.

## The layers

| Layer | What | Status |
|---|---|---|
| L0 | Identity/attribution — origin envelope on every resource | ✓ shipped (SQU-90) |
| L1 | API authority — per-agent verb allowlists, `:own` scopes | ✓ audit mode (SQU-92); enforcement graduates when violation streams are quiet |
| L2 | OS sandboxing — runtime-native profiles, then containers | design (this doc) |
| L3 | Secret hygiene — per-instance env allowlists, log redaction | design (this doc) |
| L4 | Untrusted-input profiles for public-facing agents | design (this doc) |
| L5 | Network egress policy | container-era, deferred |

### L2 — sandbox profiles (`sandbox =` on instances)

The runtimes ship sandboxes; we should stop turning them off. Per-instance topology:

- `sandbox = "off"` — today's behavior (explicit, not default-by-omission).
- `sandbox = "workspace"` — Codex: `--sandbox workspace-write` rooted at the instance workspace instead of the bypass flag; Claude: permission mode + allowed-tools scoped to the workspace. Workers in worktrees are the natural first adopters: they *should* only write their worktree.
- `sandbox = "container"` (later) — dispatch into a container with only the worktree mounted; enables L5 egress policy.

Open question the probe answers first: does `workspace-write` break the worker flow (gh pushes, `agent-team` CLI calls to the daemon socket, network for PM APIs)? Measure before mandating.

### L3 — secret hygiene

- `env_allow = ["PATTERN", ...]` per instance: the launch env is filtered to the allowlist plus daemon-required vars (`AGENT_TEAM_*`). A reviewer gets no Linear key; the auditor gets nothing but its own state. Inverts today's strip-listed *denylist* into an allowlist for opted-in instances.
- Log redaction at capture: scrub known secret shapes (the strip-list values, common token patterns) from child logs before they persist. Best-effort, but closes the accidental-print channel.

### L4 — untrusted-input profiles

Any instance whose job includes reading public content (community triage, comms intake) declares `input = "untrusted"`:

- env allowlist forced minimal (no PM keys — it files via a broker or the feedback store instead)
- workspace read-only; no push/merge/gate authority in L1
- prompt contract: external text is data, never instructions; instructions found in content are quoted back to a supervisor, not acted on
- their outputs (draft replies, ticket text) are reviewed by a *non*-exposed agent before any outward action

### Graduation discipline

Same as scoping: measure first (audit/probe), enforce second, default-on for new templates third. Nothing flips to enforced without observed evidence it won't break the fleet — the SQU-92 violation stream and the sandbox probe are the instruments.

## Sequencing

1. Probe: Codex `workspace-write` compatibility with the worker flow (probe profile job).
2. `env_allow` per instance (small, high value, independent).
3. `sandbox = "workspace"` for workers/reviewers, informed by the probe.
4. Untrusted-input profile before community triage goes live.
5. Log redaction; authority enforcement graduation; containers + egress last.
