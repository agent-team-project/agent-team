# Agent Runtime Contract

Status: design draft for SQU-146. This document is the review target for the
native-manager/runtime-agnostic execution arc. It intentionally defines the
contract before implementation.

Related design notes:

- [orchestrator.md](./orchestrator.md): daemon-owned instance lifecycle,
  topology event dispatch, mailbox, logs, managed resume, and runtime metadata.
- [security-model.md](./security-model.md): daemon-owned control plane,
  per-instance tokens, sandbox/container direction, authority brokering, and
  MCP security boundary.
- [recoverable-managers.md](./recoverable-managers.md): persistent manager
  restart, generated briefs, managed resume, and transition notifications.
- [distributed-resources.md](./distributed-resources.md): stable resource URIs,
  tokenized loopback/TCP transport, workspace/state resources, and remote-ready
  placement.
- [topology.md](./topology.md): declared instances, pipeline steps, teams,
  budgets, authority policy, and event routing.

## Summary

`agent-team` currently treats a runtime as a CLI shape:

- launch a subprocess in a workspace
- inject a prompt through argv, a prompt file, or stdin
- export `AGENT_TEAM_*` env
- expose status/inbox/gate/feedback verbs through CLI shims
- capture stdout/stderr into `child.log`
- infer session id, usage, and terminal status from runtime-specific output

That implicit shape worked for Claude, Codex, and Docker-wrapped Codex, but it
does not give LangChain, Pydantic AI, Vercel AI SDK, local model loops, or
custom binaries a documented way to become first-class runtimes.

This contract makes the boundary explicit:

1. **The daemon owns orchestration.** Jobs, queues, gates, budgets, authority,
   mailboxes, signals, status, usage ledgers, and lifecycle metadata are daemon
   resources. Runtimes never own durable control-plane truth.
2. **Runtime adapters translate daemon intent into runtime launch mechanics.**
   Claude, Codex, Docker, and future custom runtimes implement the same adapter
   contract even when their argv/config/resume behavior differs.
3. **CLI shims are universal; MCP is the ergonomic optional face.** Every
   runtime gets the same baseline through `agent-team`, `inbox`, `channel.sh`,
   and skill scripts. MCP-capable runtimes may also receive a daemon-owned,
   per-instance MCP server preconfigured by the adapter.
4. **Signals have two trust classes.** Advisory observability signals are taken
   at face value. Consequential signals are verified, measured, or overridden
   by the daemon before they affect jobs, budgets, gates, or wakeups.
5. **Reactive managers consume consequential signals.** A long-lived manager
   does not scrape logs. It subscribes to daemon-verified signals and is woken
   by a daemon-authored "manager tick" through mailbox/interrupt/managed resume.

## Goals

- Define a runtime registry and adapter lifecycle that can launch `claude`,
  `codex`, Docker-wrapped runtimes, and arbitrary future binaries uniformly.
- Preserve the current same-machine workflow while making loopback/TCP plus
  per-instance token the first-class sandbox-compatible transport.
- Replace output-stream inference as the orchestration contract with explicit,
  typed signals, without trusting agent-reported consequential claims.
- Define the daemon MCP surface and how adapters configure it at spawn time.
- Define how persistent managers subscribe to verified signals and wake
  deterministically.

## Non-Goals

- Implement the registry, MCP server, custom runtime SDK, or manager tick in
  this ticket.
- Move manager judgment into the daemon. The daemon routes verified facts; the
  manager decides what to do.
- Trust runtime or agent self-reporting for spend, authority, gate approval, or
  completion.
- Require every runtime to speak MCP. MCP improves ergonomics; the CLI/API
  contract remains the baseline.
- Define remote multi-host security beyond the already planned tokenized TCP
  shape. Multi-host graduates to mTLS/SPIFFE as described in
  `distributed-resources.md` and `security-model.md`.

## Terms

**Runtime**: the engine that executes an agent session, such as Claude Code,
Codex, a Docker image that runs Codex, a LangChain loop, or a custom binary.

**Runtime adapter**: daemon-side code or configuration that turns a normalized
launch request into runtime-specific argv/env/files/stdin, captures runtime
metadata, and translates runtime-specific observations into daemon signals.

**Instance**: a named running or resumable runtime session owned by the daemon.
Instances are still declared in topology and tracked under daemon metadata.

**Control plane**: daemon-owned job, queue, gate, mailbox, channel, status,
usage, lifecycle, budget, and authority state.

**Placement**: where a runtime process executes: same host, sandboxed process,
container, or future remote worker. Placements have local paths; resources have
stable `agt://...` URIs.

**Signal**: a runtime, agent, adapter, or daemon observation about work. Signals
are either advisory or consequential.

## Contract Layers

There are three boundaries. Keep them separate.

| Boundary | Owner | Purpose |
| --- | --- | --- |
| Topology to daemon | topology + daemon | Resolve desired instance shape, runtime selection, budgets, authority, triggers, and teams. |
| Daemon to adapter | daemon | Give a runtime-independent launch/resume/stop intent with resource IDs, paths, tokens, budgets, and capabilities. |
| Adapter to runtime | adapter | Render runtime-specific argv/env/config/stdin/MCP setup and parse runtime-specific output where needed. |

Existing code already approximates these layers:

- `internal/topology` parses declared instances and pipeline steps.
- `internal/daemon/event_spawn.go` prepares workspace, state, config, prompt,
  resource URIs, env, shims, and runtime-specific args.
- `internal/daemon/instance.go` starts and reaps child processes, records
  metadata, captures Codex session ids, and captures usage at terminal time.
- `internal/runtimeshim` installs the baseline CLI wrappers and authority shim.

The contract below makes that shape explicit and moves future runtime support
behind one adapter surface.

## Runtime Registry

Topology should be able to refer to a named runtime profile. Built-in names are
still `claude`, `codex`, and `docker`, but custom profiles use the same schema.

Sketch:

```toml
[runtimes.claude]
adapter = "claude"
command = "claude"
capabilities = ["managed_resume", "mcp_native", "otel_native"]

[runtimes.codex]
adapter = "codex"
command = "codex"
capabilities = ["managed_resume", "mcp_native", "usage_stream", "otel_native"]

[runtimes.docker-codex]
adapter = "docker"
command = "docker"
image = "agent-team:ci"
effective_runtime = "codex"
capabilities = ["container", "managed_resume", "mcp_native", "usage_stream"]

[runtimes.langchain-local]
adapter = "exec"
command = "./scripts/run-langchain-agent"
args = ["--contract", "{contract_file}"]
stdin = "none"
capabilities = ["usage_report_api", "mcp_client"]
workspace = "cwd"

[runtimes.langchain-local.env]
AGENT_TEAM_CONTRACT_FILE = "{contract_file}"
```

Instances and pipeline steps continue to select a runtime with their existing
fields:

```toml
[instances.manager]
agent = "manager"
runtime = "codex"
mcp = true

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
runtime = "langchain-local"
workspace = "worktree"
mcp = true
```

Proposed fields:

| Field | Meaning |
| --- | --- |
| `adapter` | Adapter implementation: `claude`, `codex`, `docker`, or `exec` in the first pass. |
| `command` | Host command used by the adapter. Built-ins default to the current binaries. |
| `args` | Optional exec-adapter argv template. Template variables come from the launch request. |
| `image` | Container image for image-backed runtimes. |
| `effective_runtime` | Runtime whose telemetry/log format applies when the wrapper is not the LLM runtime, e.g. Docker using Codex. |
| `capabilities` | Declared runtime capabilities. The daemon verifies what it can and degrades when missing. |
| `workspace` | Adapter expectation for cwd/mount behavior: `cwd`, `arg`, `mount`, or adapter default. |
| `mcp` | Runtime profile can support MCP. Instance/step still opts in. |
| `[runtimes.<name>.env]` | Additional non-secret env template entries. Secrets remain brokered through daemon capabilities. |

Runtime resolution precedence stays the current one:

1. explicit dispatch payload or CLI selection
2. `AGENT_TEAM_RUNTIME` env override
3. declared topology instance or pipeline step
4. agent frontmatter
5. repo `[runtime]` config
6. built-in default

The difference is that a selected runtime resolves to a registry profile, not a
hardcoded enum branch.

## Adapter Interface

The adapter interface is daemon-internal. It is not an agent API. The daemon
builds an `InstanceLaunch` once, and the adapter turns it into a process or
container launch plan.

Interface sketch:

```go
type RuntimeAdapter interface {
    Name() string
    Capabilities(context.Context, RuntimeProfile) RuntimeCapabilities

    PrepareLaunch(context.Context, InstanceLaunch) (LaunchPlan, error)
    PrepareResume(context.Context, InstanceResume) (LaunchPlan, error)
    PrepareInterrupt(context.Context, InstanceInterrupt) (LaunchPlan, error)

    CaptureSession(context.Context, RuntimeObservation) (SessionCapture, error)
    CaptureUsage(context.Context, RuntimeObservation) (UsageCapture, error)
    ClassifyExit(context.Context, RuntimeObservation) ExitClassification
}
```

`PrepareLaunch` returns only host/container mechanics:

```go
type LaunchPlan struct {
    Args        []string
    Env         []string
    CWD         string
    Stdin       string
    LogPath     string
    RuntimeDir  string
    CleanupPlan CleanupPlan
}
```

The daemon still owns:

- instance locks and duplicate-child prevention
- process start/stop/reap
- watchdogs and hard budget cutoffs
- queue draining and auto-advance
- job/gate/usage/status persistence
- token minting and authority policy
- lifecycle events

For the `exec` adapter, the launch plan points the custom binary at a contract
file. That file is a stable JSON object, versioned independently from Go
struct names.

Contract file sketch:

```json
{
  "version": "agent-runtime/v1",
  "instance": {
    "name": "worker-squ-146",
    "agent": "worker",
    "uri": "agt://deployment/instance/worker-squ-146",
    "state_uri": "agt://deployment/state/worker-squ-146",
    "state_path": "/repo/.agent_team/state/worker-squ-146"
  },
  "work": {
    "job_id": "squ-146",
    "job_uri": "agt://deployment/job/squ-146",
    "ticket": "SQU-146",
    "pipeline": "ticket_to_pr",
    "pipeline_step": "implement"
  },
  "workspace": {
    "uri": "agt://deployment/workspace/ws_...",
    "path": "/repo/.claude/worktrees/worker-squ-146",
    "branch": "squ-146-e5393793"
  },
  "daemon": {
    "url": "http://127.0.0.1:53117",
    "socket": "/repo/.agent_team/daemon.sock",
    "token_file": "/repo/.agent_team/state/worker-squ-146/daemon.token",
    "mcp": {
      "enabled": true,
      "transport": "http",
      "url": "http://127.0.0.1:53117/mcp/worker-squ-146",
      "token_file": "/repo/.agent_team/state/worker-squ-146/daemon.token"
    }
  },
  "budget": {
    "time": "45m",
    "tokens": 40000000,
    "hard": false
  },
  "prompt": {
    "mode": "file",
    "path": "/repo/.agent_team/state/worker-squ-146/runtime/system_prompt.md",
    "kickoff_path": "/repo/.agent_team/state/worker-squ-146/runtime/kickoff.md"
  },
  "capabilities": {
    "verbs": ["status.set", "inbox.*", "job.gate.set:own"],
    "mcp_tools": ["status_set", "check_inbox", "ack_inbox", "set_gate"]
  }
}
```

Rules:

- Paths are placement-local materializations. Durable identity uses `agt://...`
  URIs.
- Tokens are passed by file, never as raw env values.
- The contract file is read-only from the runtime's perspective after launch.
- The daemon may add fields. Runtimes must ignore unknown fields and must fail
  closed if `version` is unsupported.
- The adapter, not the agent, configures MCP. The runtime receives a ready-to-use
  MCP endpoint/config only when topology and capabilities opt in.

## Lifecycle

The lifecycle phases are runtime-independent:

1. **Resolve**: topology and dispatch payload resolve the target agent, runtime
   profile, workspace mode, budgets, authority, team, and resource URIs.
2. **Prepare**: daemon creates state dir, config overlay, runtime dir, skill
   symlinks, CLI shims, prompt files, contract file, token file, and optional
   MCP config.
3. **Spawn**: adapter returns a launch plan; the daemon starts the process or
   container and persists metadata before returning.
4. **Handshake**: runtime-specific session ids, thread ids, or ready events are
   captured if supported. Missing handshake does not imply failed work unless
   the runtime profile marks it required.
5. **Run**: the instance reports advisory status and may call daemon verbs
   through CLI, HTTP, or MCP. The daemon monitors process liveness, logs,
   budgets, gates, and usage.
6. **Interrupt**: persistent instances may receive a mailbox message plus a
   runtime-specific managed resume/restart. Ephemeral instances usually do not.
7. **Terminal finalize**: process exit, watchdog, stop, or reconcile produces
   daemon-owned terminal metadata. Usage capture and job reconciliation happen
   here.
8. **Reap/cleanup**: replica slots, locks, worktree policy, auto-advance, and
   signal subscriptions run after terminal metadata is durable.

Lifecycle expectations by runtime class:

| Runtime class | Launch | Resume | Usage | MCP |
| --- | --- | --- | --- | --- |
| Claude | Adapter renders Claude argv/config and session id. | Managed resume when session id is present. | Daemon records duration/turn metadata unless reliable native usage is available. | Adapter writes Claude MCP config when opted in. |
| Codex | Adapter uses `codex exec --json`, stdin prompt, workspace `-C`, and last-message sidecar. | Managed resume with recorded thread id. | Daemon parses `turn.completed` JSONL and may also consume native telemetry later. | Adapter injects Codex MCP config overlay when opted in. |
| Docker | Adapter wraps a child runtime inside a container, mounts workspace/state, and rewrites loopback daemon URL. | Depends on wrapped runtime and mounted session store. | Uses `effective_runtime` for capture. | Adapter mounts/provides MCP endpoint and token file inside the container. |
| Exec/custom | Adapter runs declared command with contract file. | Optional; profile must declare resume command or no managed resume. | Runtime reports usage through API or emits a declared usage file/stream. | Runtime may use daemon MCP if it has an MCP client. |

## Universal Runtime Environment

Every runtime receives these placement-local values when applicable:

| Env | Meaning |
| --- | --- |
| `AGENT_TEAM_ROOT` | Local `.agent_team` path for compatibility and no-daemon fallbacks. |
| `AGENT_TEAM_INSTANCE` | Instance name. |
| `AGENT_TEAM_STATE_DIR` | Local private state path. |
| `AGENT_TEAM_DAEMON_SOCKET` | Unix-socket compatibility endpoint. |
| `AGENT_TEAM_DAEMON_URL` | Token-protected loopback/TCP endpoint. Required for sandbox/container paths that cannot use AF_UNIX. |
| `AGENT_TEAM_DAEMON_TOKEN_FILE` | 0600 token file for daemon HTTP and MCP auth. |
| `AGENT_TEAM_JOB_ID` / `AGENT_TEAM_JOB_URI` | Durable job identity. |
| `AGENT_TEAM_TICKET` / `AGENT_TEAM_TICKET_URL` | PM work item identity when configured. |
| `AGENT_TEAM_PIPELINE` / `AGENT_TEAM_PIPELINE_STEP` | Pipeline context. |
| `AGENT_TEAM_BRANCH` / `AGENT_TEAM_WORKTREE` | Git placement hints. |
| `AGENT_TEAM_WORKSPACE_URI` / `AGENT_TEAM_STATE_URI` / `AGENT_TEAM_INSTANCE_URI` | Stable resource identity. |
| `AGENT_TEAM_BUDGET_TIME` / `AGENT_TEAM_BUDGET_TOKENS` | Soft allowance visibility. |

The env set is compatibility, not authority. The daemon authenticates daemon
requests using the token file and derives origin from metadata and token
identity. An agent changing env cannot widen its authority.

## Signal Model

Signals are explicit observations written by an agent, runtime, adapter, or the
daemon. The critical design rule is:

> Advisory signals improve observability. Consequential signals affect durable
> state only after daemon verification, daemon measurement, or daemon override.

This replaces fragile output-stream inference without making agent-reported
claims trusted.

### Advisory Signals

Advisory signals are accepted at face value once the caller is authenticated
and authorized for the verb. They do not by themselves release budget, satisfy
gates, close jobs, or wake managers for consequential action.

| Signal | Examples | Daemon behavior |
| --- | --- | --- |
| Phase/status | `planning`, `implementing`, `awaiting_review`, `blocked`, `idle`, `done` in `status.toml` or `status_set` | Persist/serve as observability. Staleness is judged by daemon file/API timestamps, not by trust in content. |
| Heartbeat | `heartbeat`, `last_action`, progress text | Update liveness hints. Process liveness remains daemon-measured. |
| Progress/note | "opened PR", "running tests" | Append job/lifecycle note when allowed. Does not prove artifact existence. |
| Inbox/channel ack | mailbox cursor, channel cursor | Accept as caller's read position. Does not prove semantic understanding. |
| Budget request | "need +10M tokens" | Route to manager/operator. Does not grant budget until daemon records approval/extension. |
| Feedback | friction/incidents | Store and route. Does not mutate job state unless a manager acts. |

### Consequential Signals

Consequential signals can change pipeline state, budget state, job state,
authority decisions, or manager wakeups. They are never trusted solely because
an agent or runtime said them.

| Signal | Possible reporter | Verification/override |
| --- | --- | --- |
| Process running/exited/crashed | daemon reaper, reconcile, watchdog | Daemon owns PID/process/container lifecycle and terminal metadata. Runtime exit codes are input, not final truth. Watchdog and budget cutoff may override a clean exit to crashed. |
| Usage | runtime stream, usage API, daemon parser | Daemon captures from known stream/API or records `tokens_available=false`. Agent self-report is corroborating metadata only. Budgets read daemon records. |
| Gate result | reviewer/worker via CLI/API/MCP | Daemon checks token identity, authority, target job ownership, schema, and configured infra signatures. Future configured gates may also verify CI/log refs before allowing advancement. |
| Step done/failed | agent via `job step`, process exit, pipeline reconcile | Daemon reconciles against process exit, delivery contract, PR/branch/diff artifacts, gate records, and pipeline dependencies. A clean exit without required artifact can become failed. |
| Job done/failed | manager/daemon | Daemon validates authority and terminal invariants. Merge/PM write-back remains a brokered action. |
| Authority | request header/token/env | Daemon derives origin from per-instance token and metadata. Caller-provided origin/env can fill gaps but cannot widen authority. |
| Merge/PM mutation | manager/tool request | Daemon or configured helper verifies actor/capability and target resource. Agents do not get provider secrets in the long-term model. |
| Manager wake signal | daemon | Only daemon-verified consequential events enter the subscription ledger that drives manager ticks. |

A conformant runtime must tolerate the daemon overriding its consequential
self-report. For example, a runtime may report `usage.input_tokens=0`; if the
daemon parser sees 5M tokens, the daemon record wins. A worker may set phase
`done`; if the process is still running or the delivery artifact is missing,
the job does not become done.

## Daemon Signal API

The daemon should expose one typed signal endpoint and keep existing specialized
endpoints as compatibility wrappers.

HTTP sketch:

```http
POST /v1/signal
Authorization: Bearer <instance-token>
Content-Type: application/json
```

```json
{
  "type": "status.phase",
  "class": "advisory",
  "instance": "worker-squ-146",
  "job_id": "squ-146",
  "payload": {
    "phase": "implementing",
    "description": "Writing runtime contract design doc"
  },
  "observed_at": "2026-07-07T10:00:00Z"
}
```

Response:

```json
{
  "accepted": true,
  "signal_id": "sig_...",
  "class": "advisory",
  "effective": {
    "stored": true
  }
}
```

For consequential signals:

```json
{
  "accepted": true,
  "signal_id": "sig_...",
  "class": "consequential",
  "effective": {
    "verified": true,
    "job_status": "running",
    "step_status": "done"
  },
  "overrides": []
}
```

If the daemon overrides:

```json
{
  "accepted": true,
  "signal_id": "sig_...",
  "class": "consequential",
  "effective": {
    "verified": false,
    "job_status": "failed"
  },
  "overrides": [
    {
      "field": "job_status",
      "reported": "done",
      "effective": "failed",
      "reason": "delivery artifact missing"
    }
  ]
}
```

Initial signal types:

| Type | Class | Compatibility wrapper |
| --- | --- | --- |
| `status.phase` | advisory | status skill / `status.toml` |
| `status.heartbeat` | advisory | status skill `last_action` |
| `inbox.ack` | advisory/operational | inbox skill |
| `channel.ack` | advisory/operational | channel skill |
| `job.note` | advisory | `agent-team job note` |
| `budget.request_extension` | advisory request | `agent-team job extend` flow before approval |
| `usage.report` | consequential | new API for runtimes with no parseable stream |
| `gate.result` | consequential | `agent-team job gate set` |
| `step.finish` | consequential | `agent-team job step --status done|failed` |
| `job.finish` | consequential | `agent-team job close` / manager merge path |
| `runtime.ready` | consequential handshake | adapter-specific session capture |
| `runtime.exit` | consequential daemon event | reaper/reconcile/watchdog |

Compatibility wrappers should eventually write through the same signal ledger
so MCP, CLI, and runtime-native reports share one audit path.

## MCP Face Of The Daemon

MCP is an optional agent-facing interface over the daemon API. It is not a
second authority model.

Principles:

1. The daemon serves MCP. Agents do not mount arbitrary project-generated MCP
   servers for control-plane verbs.
2. The adapter configures MCP at spawn. No agent hand-edits MCP config or sees a
   raw server list it is expected to assemble.
3. The visible MCP toolset is generated from topology authority and instance
   capabilities. Discovery is the allowlist: tools the instance cannot call are
   absent.
4. CLI shims remain the universal baseline and no-daemon fallback. MCP tools are
   typed wrappers over the same daemon verbs.
5. Consequential MCP tool calls still pass through daemon verification. A typed
   `finish_step` call is easier to validate than a shell command, but it is not
   inherently trusted.
6. Security guidance about tool poisoning applies primarily to third-party MCP
   servers an agent might mount. The daemon MCP server is local/daemon-owned,
   generated from pinned topology, and authenticated with the same per-instance
   token as HTTP.

Transport:

- Same-host runtimes may use MCP over stdio if the adapter can spawn a daemon
  MCP bridge process safely.
- Sandboxed/container runtimes should use MCP over loopback HTTP/SSE with the
  per-instance token file.
- Future remote placements reuse the TCP resource/capability model and replace
  bearer-only loopback assumptions with mTLS.

Adapter setup examples:

| Runtime | Adapter responsibility |
| --- | --- |
| Claude | Write generated MCP config pointing at the daemon endpoint with instance token and generated toolset. |
| Codex | Inject `mcp_servers` through a config overlay or launch `-c` args, using token-file auth. |
| Docker | Mount token file/state, rewrite loopback URL to host gateway, and provide MCP config inside the container. |
| Exec/custom | Put MCP endpoint/token/toolset in the contract file; the runtime decides how to mount its MCP client. |

Initial MCP tools:

| Tool | Underlying daemon verb | Class |
| --- | --- | --- |
| `status_set` | `status.phase` | advisory |
| `heartbeat` | `status.heartbeat` | advisory |
| `check_inbox` | mailbox read | advisory read |
| `ack_inbox` | `inbox.ack` | advisory/operational |
| `send_message` | `inbox.send` | advisory/operational |
| `channel_recv` / `channel_ack` / `channel_publish` | channel API | advisory/operational |
| `job_show` | job read | read |
| `job_note` | `job.note` | advisory |
| `request_budget_extension` | budget request | advisory request |
| `report_usage` | `usage.report` | consequential |
| `set_gate` | `gate.result` | consequential |
| `finish_step` | `step.finish` | consequential |
| `submit_feedback` | feedback API | advisory |
| `publish_event` | `/v1/event` | consequential; manager/operator only |

Tool schemas should use closed enums for statuses, phases, gate names where
known, and resource ids. Free-form strings stay bounded and size-capped.

## Manager Tick

A native manager should be long-lived and reactive without polling logs or
depending on another human session. The daemon already owns enough verified
state to wake it.

### Consequential Signal Subscriptions

Persistent instances may declare signal subscriptions in topology. A
subscription matches daemon-verified events, not arbitrary agent text.

Sketch:

```toml
[instances.manager]
agent = "manager"
ephemeral = false
restart = "on-failure"
brief = true
mcp = true

[[instances.manager.signal_subscriptions]]
name = "delivery-attention"
class = "consequential"
events = [
  "job.step.done",
  "job.step.failed",
  "gate.result",
  "budget.exceeded",
  "queue.dead",
  "delivery_artifact.missing",
  "pr.review_requested",
  "pr.merged"
]
scope = "team"
wake = "tick"
coalesce = "5s"
```

Matching uses the same small topology match vocabulary where possible:

- `scope = "team"` restricts to resources owned by the manager's team.
- `match.pipeline = "ticket_to_pr"` or `match.job = "squ-146"` narrows further.
- `wake = "tick"` requests daemon coalescing and wakeup.
- `wake = "mailbox"` records messages without interrupt/resume.
- `wake = "none"` records only a cursor-readable signal stream.

### Tick Event

A manager tick is a daemon-authored wake event containing verified facts and
links, not instructions copied from public input.

Mailbox body sketch:

```json
{
  "event": "manager.tick",
  "subscription": "delivery-attention",
  "cursor": "sig_01j...",
  "signals": [
    {
      "id": "sig_01j...",
      "type": "job.step.done",
      "job_id": "squ-146",
      "pipeline": "ticket_to_pr",
      "step": "implement",
      "instance": "worker-squ-146",
      "summary": "implement step exited cleanly; PR metadata present",
      "links": {
        "job_uri": "agt://deployment/job/squ-146",
        "instance_uri": "agt://deployment/instance/worker-squ-146"
      }
    }
  ],
  "requested_action": "inspect the verified signals and decide the next orchestration action"
}
```

Wake mechanics:

1. A consequential signal is verified and appended to the daemon signal ledger.
2. The subscription resolver finds interested persistent instances.
3. The daemon coalesces matching signals for a short window to avoid interrupt
   storms.
4. The daemon writes a `manager.tick` mailbox message and advances no cursors.
5. If the instance is running and supports interrupt, the daemon calls the
   managed interrupt path. If stopped/crashed and restart policy allows it, the
   daemon starts/resumes it with the tick included in the resume prompt or
   mailbox.
6. The manager handles the tick and acks the cursor through CLI/MCP/API.

Delivery semantics:

- At-least-once. A manager must treat signal ids as idempotency keys.
- Ordered per subscription cursor, not globally ordered across all signals.
- Cursor ack is advisory/operational: it records the manager's read position.
  It does not mutate the underlying job.
- The generated recoverable brief includes unacked consequential signals so a
  fresh or recovered manager can catch up.

This extends `recoverable-managers.md`: restart policy and managed resume keep
the manager alive; manager tick supplies the deterministic event source that
lets it act after verified changes.

## Authority And Security

The contract relies on the security model rather than replacing it.

- Per-instance token files authenticate daemon HTTP and MCP. Token values do
  not live in env.
- The daemon derives origin from token identity and metadata. Caller-provided
  origin headers/env are hints only.
- The runtime shim remains a defense-in-depth CLI gate for runtimes that call
  `agent-team`.
- MCP tool generation mirrors the same authority allowlist. Hidden tools are
  the default user experience; daemon-side checks still enforce/audit.
- Agents should not receive provider API secrets. Provider operations become
  daemon-brokered verbs or narrow helper operations.
- Control-plane mutation through direct files is legacy/no-daemon fallback.
  Sandboxed/container/remote placements should use daemon API/MCP only.
- Runtime-native sandboxes and containers receive only workspace, state,
  token-file, runtime dir, and required caches/mounts.

Consequential signal verification is also a security boundary. A compromised or
confused worker can set status text to anything, but it cannot make a job done,
mint budget, widen authority, or wake a manager with arbitrary consequential
facts without crossing daemon verification.

## Usage Reporting For Custom Runtimes

Built-in Codex usage is parseable from JSONL today. Other runtimes need a
language-neutral usage-report path.

`usage.report` sketch:

```json
{
  "type": "usage.report",
  "class": "consequential",
  "instance": "worker-squ-146",
  "job_id": "squ-146",
  "payload": {
    "source": "runtime",
    "turn_id": "turn-7",
    "input_tokens": 12000,
    "cached_input_tokens": 3000,
    "output_tokens": 1800,
    "reasoning_output_tokens": 400,
    "model": "custom-model",
    "at": "2026-07-07T10:00:00Z"
  }
}
```

Rules:

- Usage reports are incremental and idempotent by `(instance, turn_id)` or a
  daemon-issued report id.
- The daemon records the source and whether token values are runtime-native,
  adapter-parsed, or unavailable.
- When both runtime reports and daemon parsing exist, daemon policy chooses the
  authoritative source and records overrides.
- Missing usage is not zero usage. It is `tokens_available=false`.
- Hard token cutoffs can only enforce from live trusted streams. If a runtime
  only reports at terminal time, it can participate in accounting but not live
  token watchdogs.

## Conformance

A runtime profile is conformant at one of three levels.

| Level | Requirements | Examples |
| --- | --- | --- |
| L0 baseline | Launches with contract env/file, reads prompt, can call CLI/API for status/inbox/gates, exits with logs. No managed resume or native usage required. | Simple exec adapter, shell/Python loop. |
| L1 managed | L0 plus stable session id, managed resume or restart behavior, and usage reporting or parseable usage stream. | Codex, Claude where supported. |
| L2 reactive | L1 plus adapter-configured MCP and signal subscription support for persistent instances. | Native manager runtime. |

Reference conformance test:

1. Launch a 50-line fixture runtime through `adapter = "exec"`.
2. Fixture reads the contract file, calls `status_set`, sends/acks inbox,
   reports one usage turn, sets a test gate, writes a last message, and exits.
3. Daemon verifies metadata, usage ledger, gate record, signal ledger, and
   terminal job reconciliation.
4. Same fixture runs once with CLI shims and once with MCP tools when enabled.

The reference adapter should stay intentionally small. Its purpose is to prove
the contract, not to become a framework.

## Migration Plan

1. **Documented contract**: land this design.
2. **Registry parser**: extend topology/config parsing for `[runtimes.*]` and
   `mcp`/subscription declarations while preserving current enum behavior.
3. **Adapter extraction**: move Claude/Codex/Docker launch branches behind the
   adapter interface without changing behavior.
4. **Signal ledger**: add `/v1/signal` and route status/gate/usage/step wrappers
   through it, initially preserving current files as storage.
5. **Exec adapter + fixture**: implement L0 custom runtime conformance.
6. **MCP daemon server**: expose generated per-instance toolsets and have
   Claude/Codex adapters configure them at spawn.
7. **Manager tick**: add consequential signal subscriptions, coalescing, cursor
   ack, mailbox tick, and managed interrupt/resume wakeups.
8. **Authority enforcement graduation**: after audit data is quiet, use the same
   policy for CLI, HTTP, and MCP.

Each step is independently reviewable. The current `claude`, `codex`, and
`docker` paths should remain behaviorally unchanged until the adapter extraction
has parity tests.

## Open Questions

- Should the first MCP transport be HTTP/SSE only, or should the daemon also
  ship a stdio bridge for runtimes that strongly prefer stdio MCP?
- Which consequential gates should be purely schema/authority verified in v1
  versus backed by configured verifiers such as GitHub checks or log refs?
- Does `signal_subscriptions` belong directly under instances, teams, or both?
  Instance-level is clearest for manager tick; team-level may reduce duplication.
- Should usage reports be accepted mid-run from all custom runtimes, or only
  from profiles that declare `usage_report_api` and pass a conformance test?
- What is the minimum L1 resume contract for framework runtimes that do not have
  native conversation/session stores?
