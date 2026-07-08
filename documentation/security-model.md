# Security model

This document defines the security model for running an autonomous
`agent-team` fleet against a repository. It is the capability companion to
`resource-constraints.md`: budgets bound spend, while this model bounds what an
agent can read, write, call, and leak.

Status: design with first enforcement slices landed. Identity/origin and
API-verb authority enforcement exist today. Runtime sandboxing, secret
allowlists, and untrusted-input profiles are staged behind probes and narrow
implementation slices.

## Goals

- A confused or prompt-injected agent must not be able to mutate shared control
  plane state, exfiltrate credentials, or widen its own authority.
- The same topology declaration that says who may do work should say what that
  runtime can touch: filesystem, network, daemon/API verbs, and secrets.
- Security defaults must graduate in this order: measure, enforce for selected
  instances, then default-on for new templates. No fleet-wide enforcement flips
  without evidence from probes or audit logs.

## Non-goals

- This is not a defense against a malicious local user with the same OS account.
  Today all agent runtimes inherit the operator's user account.
- This does not attempt multi-tenant isolation until container or microVM
  workspaces exist.
- This does not replace provider-side controls such as GitHub branch
  protection, Linear permissions, or runtime vendor authentication.

## Threat model

The expected failure is confusion, not malice. Agents are cooperative, but they
read untrusted text, run tools, and make mistakes.

1. Prompt injection through public input. Public issues, PRs, discussions, and
   comments are data, but they may contain instructions aimed at the agent.
2. Secret exposure. Credentials may exist in `.env`, runtime keychains, shell
   env, provider helpers, logs, command lines, and daemon state snapshots.
3. Control-plane mutation. Worktree isolation does not protect `.agent_team/`.
   Without a split, a worker can edit job records, mailbox files, queues,
   gates, and daemon metadata directly.
4. Authority creep. API allowlists can deny verbs, but a process with broad
   filesystem and network access can still try alternate paths.
5. Egress. If an agent can read a secret and reach arbitrary network
   destinations, prompt injection can turn that into exfiltration.

## Layer model

| Layer | Purpose | Current status |
| --- | --- | --- |
| L0 identity | Every daemon resource and event carries trusted origin metadata. | Shipped. |
| L1 API authority | Per-agent/team/instance verb allowlists; destructive verbs can enforce. | Shipped for destructive verbs. |
| L2 runtime sandbox | OS/runtime boundary for filesystem and process capabilities. | Design/probe. |
| L3 secret hygiene | Secrets are not committed, persisted, or injected broadly. | Partial; this doc codifies the invariants. |
| L4 untrusted input | Public-input readers are structurally separated from actors. | Design. |
| L5 egress | Network allowlists/off-by-default for untrusted workloads. | Container-era, deferred. |

The layers are intended to compose. L1 says a worker cannot call `job.merge`;
L2 should make it unable to forge the same outcome by writing job files; L3
should leave it without raw provider credentials; L4 should keep raw public text
away from the privileged actor that can publish, merge, or spend credentials.

## Runtime sandbox tiers

Topology should select a sandbox tier per instance. The tier controls the child
runtime's filesystem, network, and secret surface. Names below describe the
policy contract; exact CLI flags are runtime-specific.

| Tier | Filesystem | Network | Secrets | Intended use |
| --- | --- | --- | --- | --- |
| `sandbox = "off"` | Full user-account filesystem, including the repo, home directory, and `.agent_team/`. | Ambient network. | Ambient shell env plus any `.env` the process reads. | Legacy/trusted-only mode while probes and migrations run. Must be explicit, not default-by-omission. |
| `sandbox = "workspace"` | Write only the worktree, the instance state dir, and declared cache/tmp dirs. Read access should be limited to repo content needed for the task. No direct writes to shared `.agent_team/jobs`, queue, mailbox, daemon, or outbox state. | Runtime-required network only: GitHub/Linear/git endpoints and the daemon API. Prefer daemon TCP-loopback plus token over filesystem sockets for sandbox compatibility. | No raw provider keys by default. Runtime receives non-secret context env, a daemon token file path, and only instance-declared `env_allow` entries. | Default target for workers and reviewers. |
| `sandbox = "wrapped"` | Daemon-generated OS sandbox around the runtime: deny-by-default, with explicit worktree/state/tmp/cache write roots. | Explicit host/port or service allowlist. Unix socket access only when the OS sandbox can represent it safely. | Same as `workspace`; no new secrets. | Semi-trusted workloads when runtime-native sandboxing is too weak or unavailable. |
| `sandbox = "container"` | Container/microVM with mounted worktree and state dir. No host home. Shared control plane reached only through the daemon API. | Off by default, then allowlisted egress. | Brokered secret handles and short-lived capability tokens, mounted as files or fetched through authorized daemon verbs. | Untrusted code, public-input processors, and future remote/distributed workers. |

### Runtime-specific notes

Codex and Claude both ship native sandbox controls, so the first implementation
should drive those controls instead of writing an orchestrator sandbox from
scratch. The worker probe on macOS found that naive Codex
`workspace-write` is not enough: it blocked git index writes, cache writes,
state-dir writes, network operations, and AF_UNIX daemon socket access. For
Codex on macOS, `workspace` therefore requires declared writable roots for the
worktree, state, caches, and temp space, plus network access for provider/git
traffic and a sandbox-compatible daemon transport. Re-probe Linux/bubblewrap
before generalizing that result.

Do not combine autonomous workers with approval modes that escalate outside the
sandbox. Autonomous runs should use non-interactive approval policy inside the
sandbox rather than asking for operator approval to escape it.

## Control-plane and workspace split

The daemon owns the shared control plane. Agent runtimes may write only their
workspace and their own state dir. Durable shared mutations go through
authority-checked daemon/API verbs.

The split is required because `.agent_team/` contains both private per-instance
state and global state:

- Per-instance state: `.agent_team/state/<instance>/`
- Shared control plane: jobs, queue, mailbox, outbox, daemon metadata, locks,
  budget ledgers, and gate records

Until this split is enforced by L2, direct filesystem writes remain a bypass
around L1 authority. Once workers can only write their worktree and own state
dir, the API allowlist and filesystem boundary describe the same security
contract.

## Secret hygiene

Secrets are credentials, tokens, cookies, authorization headers, private keys,
webhook URLs, and any value that could authenticate to an external service or
spend another user's authority.

The invariants:

1. `.agent_team/config.toml` stores non-secret configuration only: provider
   owner/repo IDs, team IDs, project IDs, labels, runtime names, and secret
   handles or env-var names. Literal credentials belong in `.env` today and in
   brokered secret handles as that path lands.
2. Provider helpers may resolve credentials from process env or `.env`; they
   must not require credentials to be committed into `.agent_team/`.
3. Daemon-persisted launch env and logs must not contain raw secret values.
   Children receive `AGENT_TEAM_DAEMON_TOKEN_FILE`, not a bearer token value in
   env. Future provider auth should follow the same file/handle pattern.
4. Instance launch env should move from denylist stripping to explicit
   `env_allow = [...]`. `AGENT_TEAM_*` context variables are for non-secret job,
   branch, state, and origin metadata. Secret env vars are opt-in and should
   disappear as brokered verbs replace direct provider keys.
5. Runtime subscription authentication stays runtime-owned. For Codex, ordinary
   workers should rely on the installed Codex subscription/session auth, not
   repo-local `OPENAI_API_KEY` or committed runtime credentials.
6. Command-line arguments, status files, job events, PR bodies, and comments
   must never echo secret values. Diagnostics report paths, key names, and
   remediation, not the candidate secret.
7. Log redaction is defense in depth, not the primary boundary. It should scrub
   known strip-list values and common token shapes before child logs persist,
   but the stronger fix is not giving the child the secret.

Concrete slice: `agent-team doctor` fails when `.agent_team/config.toml`
contains secret-looking keys or high-confidence secret literals. This is not a
full repository secret scanner; it enforces the specific config invariant above.

## Untrusted-input profile

Any instance that reads public or third-party text declares
`input = "untrusted"` once the topology field exists. Until then, topology and
prompt contracts must treat those agents as untrusted-input processors.

Rules for untrusted-input processors:

- They receive no provider write credentials and no merge/push/gate authority.
- They do not see raw private data that is unnecessary for summarization.
- They emit fixed-schema, size-capped summaries or draft artifacts.
- Text from issues, PRs, web pages, Discord, email, and comments is data. If it
  contains instructions, the agent quotes or summarizes them as input content;
  it does not execute them.
- A separate actor instance, which does not see raw public text, performs
  privileged writes after review.

This reader/actor split is the structural mitigation for prompt injection. A
prompt rule alone is insufficient when the same process has private data,
untrusted input, and network egress.

## Authority and capability rules

- Every daemon/API request resolves an origin from trusted runtime metadata and
  its capability token, not from caller-provided env alone.
- A spawned child can receive only an attenuated subset of the parent's verbs.
- Unknown verbs are denied closed-world.
- Authority decisions are logged in audit mode before moving to enforcement.
- Provider write-back should be brokered through daemon verbs where possible so
  agents do not need raw provider tokens.

The existing runtime shim enforcement is part of L1. It is useful defense in
depth, but it is not the whole boundary until L2 prevents filesystem bypasses.

## Graduation plan

1. Keep authority violation logging on for new verbs and promote stable
   destructive verbs to enforcement.
2. Land config and launch-env secret checks: doctor checks for committed config
   secrets, env snapshots strip/redact secrets, and `env_allow` validates
   declared process-env needs.
3. Add the sandbox-compatible daemon transport required by Codex
   `workspace-write` on macOS.
4. Enable `sandbox = "workspace"` for a small worker/reviewer cohort with
   verified git, provider API, test, push, and status flows.
5. Make `workspace` the default for new worker/reviewer instances once the
   cohort has no blocking violations.
6. Add untrusted-input topology profiles before public community triage or
   comms intake can publish outward.
7. Add container workspaces and egress policy for untrusted code and future
   distributed compute.

## Review checklist for security-sensitive changes

- Does this change give an agent a new filesystem, network, provider, or daemon
  capability?
- Is the capability declared in topology and enforced at the daemon/API
  boundary?
- Can the same effect be achieved by direct file writes outside the API?
- Does any raw secret move into `.agent_team/config.toml`, daemon env, command
  args, logs, status, events, PR bodies, or comments?
- Does any instance combine raw public input, private data, and egress?
- Is enforcement backed by a probe, an audit window, or a narrow cohort rollout?
