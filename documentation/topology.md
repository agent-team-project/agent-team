# Topology declaration (design sketch)

**Status**: design sketch, not yet built. Captures the v1.2+ model for declaring **which instances exist** and **when each gets called**. Companion to [`templates.md`](./templates.md) (authoring/distribution) and [`orchestrator.md`](./orchestrator.md) (lifecycle/runtime). The runtime side of this lands with the daemon; the schema side ships earlier in templates.

## What it is

`docker-compose` for agents. A repo declares its named instances and the events that route to each. The daemon resolves events to dispatches.

| Concern | Today | With topology |
|---|---|---|
| **Which instances exist** | Whatever the user has spawned ad-hoc via `agent-team run <agent> --name <name>`. No declarative source of truth. | Declared in `instances.toml`. `agent-team instance up` brings up the declared persistent instances. |
| **What each is configured for** | Single repo-wide `config.toml`. All instances of `ticket-manager` share the same Linear project. | Per-instance config overrides in `instances.toml`. Multiple `ticket-manager` instances can target different Linear projects. |
| **When each gets invoked** | Hand-written. User runs `agent-team run`, or one agent dispatches another via the `assign-worker` skill / Claude Code primitives. | Triggers declared in `instances.toml`. Daemon resolves user invocations, ticket webhooks, PR events, scheduled timers, channel messages, and inter-agent dispatches against the trigger table. |

## Why this model

Three concrete needs not met by the today-style ad-hoc model:

1. **Multiple instances of the same agent with different settings.** "Two `ticket-manager`s in one repo, one routing to Linear project A, one to project B." The motivating case from the design conversation. Today's repo-global `config.toml` doesn't support per-instance overrides.
2. **Predictable bring-up.** A new contributor cloning the repo wants `agent-team instance up` to start everything that should be running. Today they'd have to know to spawn each one by hand and remember the right flags.
3. **Event-driven dispatch.** Ticket webhooks, scheduled timers, PR events, channel publishes — all need to route to the right instance without a human in the loop. Today, dispatch is in-process: a manager spawns a worker via Claude Code's `Agent` tool. With the daemon and topology, dispatch becomes "any source of events → daemon → declared trigger → instance."

None of these are needed for the simplest "run a manager and chat" workflow — that path keeps working. Topology is the layer for users who outgrow ad-hoc spawning.

## Concepts

### Instance (declared)

A named runtime spawn of an agent, declared with config and triggers. Distinct from today's "instance" (which is just whatever `--name` you typed at `agent-team run`). After topology lands, a *declared* instance has:

- A canonical name (`manager`, `tm-platform`, `worker`).
- A reference to the agent template it runs.
- An `ephemeral` flag (true → spawn-on-demand, exit on completion; false → long-lived, brought up at `instance up`).
- Optional config overrides on top of the repo's `config.toml`.
- Zero or more triggers — events that should invoke this instance.

### Trigger

An event-matcher pair. Says "when an event of type X arriving with these properties, route it to this instance." The daemon owns the matching.

Event types in v1.2:

| Type | Source | Payload |
|---|---|---|
| `user_invocation` | `agent-team run <name>` from a human session | name, optional kickoff prompt |
| `agent.dispatch` | One instance dispatching another (e.g. manager → worker) via the orchestrator API | source instance, target name, kickoff |
| `ticket_webhook` | Linear webhook | event type (created/updated/commented), ticket fields (project, label, state, assignee) |
| `pr_webhook` | GitHub webhook | event type (opened/review-requested/review-submitted/check-failed), PR metadata |
| `schedule` | Cron-like timer in the daemon | cron expression, optional jitter |
| `channel.message` | Subscribed channel receives a publish (see future channels work) | channel name, message body |

Each event source is its own ticket; v1.2 launch likely starts with `user_invocation` + `agent.dispatch` + `schedule` (the simplest three). Webhook sources require an HTTP listener and authn, which can come in v1.3.

## Schema (`instances.toml`)

Lives at the template root (defaults shipped by template authors) and at `.agent_team/instances.toml` (consumer overrides). Same layered model as `config.toml`: template default → repo override.

```toml
[instances.manager]
agent       = "manager"
ephemeral   = false
description = "User-facing entry point. Coordinates ticket-managers and workers."

[[instances.manager.triggers]]
event = "user_invocation"
# match defaults to "any" — no filter

[instances.tm-platform]
agent       = "ticket-manager"
ephemeral   = false
description = "Routes Platform-team tickets."

[instances.tm-platform.config.linear]
project_id  = "3d07030a-a372-41a2-b01e-1b4116d0f151"

[[instances.tm-platform.triggers]]
event   = "ticket_webhook"
match.project = "Platform"
match.event   = ["created", "updated"]   # list = OR

[instances.tm-mobile]
agent       = "ticket-manager"
ephemeral   = false

[instances.tm-mobile.config.linear]
project_id  = "50b6cd55-5760-4fd3-9bbe-acb17e544aa2"

[[instances.tm-mobile.triggers]]
event   = "ticket_webhook"
match.project = "Mobile"

[instances.worker]
agent     = "worker"
ephemeral = true        # spawn per dispatch
replicas  = 3            # max 3 concurrent

[[instances.worker.triggers]]
event  = "agent.dispatch"
match.target = "worker"
```

### Field reference

| Field | Required | Default | Meaning |
|---|---|---|---|
| `agent` | yes | — | Agent template directory under `.agent_team/agents/`. Must exist after `init`. |
| `ephemeral` | no | `false` | If `true`, spawn-on-trigger and exit on completion. If `false`, brought up at `instance up`, runs until stopped. |
| `description` | no | empty | Human-readable. Shown in `instance ps`. |
| `config.<dotted.key>` | no | — | Override values for the resolved per-instance config (layers between repo and CLI flags). Same dotted-key syntax as parameter declarations in `template.toml`. |
| `replicas` | no | `1` | Max concurrent runs. Ephemeral only — for persistent, this is implicitly 1. |
| `triggers` | no | empty | List of trigger blocks. Empty triggers list → instance only invokable by explicit `agent-team run <name>`. |

### Trigger field reference

| Field | Required | Meaning |
|---|---|---|
| `event` | yes | Event type from the table above. |
| `match.<key>` | no | Filter on payload keys. Single value = exact match; list = OR-of-values. Multiple `match.<key>` entries = AND across keys. |

### Match-expression scope (v1.2)

Match expressions are intentionally limited to a small DSL:
- Single value: `match.project = "Platform"` → exact equality.
- List: `match.project = ["Platform", "Infra"]` → membership.
- Multiple keys AND: `match.project = "Platform"` + `match.label = "bug"` → both must hold.

No regex, no boolean operators across keys, no negation in v1.2. If users need richer matching, they declare multiple instances with overlapping triggers (the daemon dispatches to all matching instances).

## Layered config resolution chain

`templates.md` defines a four-layer chain for parameter resolution. Topology adds the **per-instance declared config** layer:

```
1. CLI flags                              (--set linear.project_id=<x>)
2. Per-instance config file               (.agent_team/state/<instance>/config.toml)
3. Per-instance declared overrides        (instances.toml [instances.<name>.config])  ← NEW
4. Repo config                            (.agent_team/config.toml)
5. Template defaults                      (template.toml [[parameter]] defaults)
```

The new layer (#3) sits between the repo config and per-instance state file: the **declared** override is what the template/repo author intends; the per-instance state file is the per-runtime opportunity to override further (e.g. by `agent-team run --set` flags persisting their values).

In practice, declared overrides and state files rarely conflict — declared overrides set the topology-time intent, state files capture runtime tweaks.

## CLI surface additions

```
agent-team instance up [<name>...]
    Bring up declared persistent instances. With no args: all non-ephemeral declared instances.
    Idempotent — already-running instances are left alone.

agent-team instance down [<name>...]
    Gracefully stop declared persistent instances. With no args: all running.

agent-team instance ls
    List declared instances and their state (running / stopped / never-spawned / crashed).
    Joins instances.toml + daemon process state + status.toml from each state dir.

agent-team instance ps
    Same as `ls` but filtered to currently-running.

agent-team instance show <name>
    Print the declared instance's resolved config + triggers + current state.

agent-team event publish <type> [--payload <json>]
    Manual event injection — useful for testing trigger matching.
    The daemon resolves the event against declared triggers and dispatches accordingly.
```

`agent-team run <agent>` continues to work for ad-hoc spawning. It's now sugar for "publish a `user_invocation` event with target=<agent>". If a declared instance with name = `<agent>` exists, the run targets that declared instance (with its config); otherwise the agent template is spawned with a generated instance name.

## Daemon API additions

The orchestrator daemon (see [`orchestrator.md`](./orchestrator.md)) gains:

```
POST /event
    { "type": "ticket_webhook", "payload": { "project": "Platform", "event": "created", ... } }
    → { "matched": [<instance-names>], "dispatched": [{instance_id, started_at}, ...] }

GET /topology
    → declared instances + triggers, as parsed from the layered instances.toml

POST /topology/reload
    Re-reads instances.toml. Useful after editing without restarting the daemon.
```

Existing endpoints (`/dispatch`, `/message`, `/instances`, `/logs`) stay the same. `/event` is the public trigger entry point; `/dispatch` becomes its private implementation detail.

## Worked example: multi-ticket-manager routing

The motivating case from the design conversation: a user with two services in one repo wants tickets routed to two different Linear projects.

### `instances.toml` (consumer-authored)

```toml
[instances.manager]
agent     = "manager"
ephemeral = false

[[instances.manager.triggers]]
event = "user_invocation"

[instances.tm-platform]
agent     = "ticket-manager"
ephemeral = false

[instances.tm-platform.config.linear]
project_id = "3d07030a-a372-41a2-b01e-1b4116d0f151"

[[instances.tm-platform.triggers]]
event         = "ticket_webhook"
match.project = "Platform"

[instances.tm-mobile]
agent     = "ticket-manager"
ephemeral = false

[instances.tm-mobile.config.linear]
project_id = "50b6cd55-5760-4fd3-9bbe-acb17e544aa2"

[[instances.tm-mobile.triggers]]
event         = "ticket_webhook"
match.project = "Mobile"

[instances.worker]
agent     = "worker"
ephemeral = true
replicas  = 3

[[instances.worker.triggers]]
event        = "agent.dispatch"
match.target = "worker"
```

### Bringing it up

```sh
$ agent-team instance up
Starting manager (manager)         ✓
Starting tm-platform (ticket-manager) ✓
Starting tm-mobile (ticket-manager)   ✓
worker (ephemeral, replicas=3) — spawn-on-trigger, not started
```

`agent-team instance ls`:

```
NAME           AGENT           STATE     EPHEMERAL  TRIGGERS                          PHASE
manager        manager         running   no         user_invocation                   idle
tm-platform    ticket-manager  running   no         ticket_webhook (project=Platform) idle
tm-mobile      ticket-manager  running   no         ticket_webhook (project=Mobile)   idle
worker         worker          —         yes (3)    agent.dispatch (target=worker)    —
```

### Event flowing through

A Linear ticket lands in the Platform project. The webhook hits the daemon:

```
POST /event
    { "type": "ticket_webhook",
      "payload": { "project": "Platform", "event": "created", "id": "PLAT-42", ... } }

→ { "matched": ["tm-platform"],
    "dispatched": [{ "instance_id": "...", "started_at": "..." }] }
```

`tm-platform` wakes up (it's persistent — already running, the daemon `SendMessage`s it the event payload), reads the ticket, files / triages / etc. against its declared `linear.project_id = 3d07030a-...`. `tm-mobile` is unaffected.

If the manager later dispatches a worker via `assign-worker`:

```
POST /event
    { "type": "agent.dispatch",
      "payload": { "source": "manager", "target": "worker", "kickoff": "implement SQU-42" } }

→ { "matched": ["worker"],
    "dispatched": [{ "instance_id": "worker-squ-42", "started_at": "..." }] }
```

A fresh worker spawns (ephemeral, generated name from the kickoff). When it opens its PR and exits, the slot frees up — capped at the declared `replicas = 3`.

### Inspecting and stopping

```sh
$ agent-team instance ps
NAME              AGENT           UPTIME  PHASE              SUMMARY
manager           manager         3h      idle               waiting on user
tm-platform       ticket-manager  3h      idle               last triaged PLAT-42 12m ago
tm-mobile         ticket-manager  3h      idle               —
worker-squ-42     worker          8m      implementing       Porting parameter substitution

$ agent-team instance down tm-mobile
Stopping tm-mobile ... ✓ (state preserved at .agent_team/state/tm-mobile/)
```

## Open design questions

1. **Match-expression DSL scope.** v1.2 starts with the small TOML-key DSL (single value / list / multiple AND-keys). Users may eventually want regex (`match.title ~ "^\\[urgent\\]"`) or simple boolean ops. Defer to v1.3 once we see what real workloads look like.

2. **Inter-agent dispatch migration.** Today, `manager` dispatches `worker` via the `assign-worker` skill (which uses Claude Code's `Agent` tool with `team_name`). With topology, dispatch becomes `POST /event {type: "agent.dispatch", target: "worker"}`. Two paths:
   - **Compat shim**: keep `assign-worker` as the user-facing skill, rewrite its implementation to POST to the daemon. Skill API unchanged.
   - **Direct migration**: agents call the daemon API directly via a new `dispatch` skill; deprecate `assign-worker`.
   First is more incremental and probably right for v1.2.

3. **Replicas semantics.** For ephemeral instances with `replicas = N`: do we queue events that arrive while at capacity, or reject them? Probably queue with a configurable cap; rejection is bad UX for the dispatcher (manager would have to retry).

4. **State preservation on `instance down`.** Currently `.agent_team/state/<instance>/` survives stop/start cycles by design. Should `instance down --rm` exist (Docker-style "remove all state")? Probably yes for ephemeral; probably no for persistent (would clobber journal/goals/progress).

5. **Topology hot-reload.** `agent-team instance reload` re-parses `instances.toml` and applies diffs (start newly-declared, stop newly-undeclared, restart changed). Implementation has a tricky case: a running instance whose declared config changed — graceful restart, or wait for current work to drain? Defer the policy to v1.2 PR; default likely "warn, don't auto-restart, require explicit `instance restart <name>`."

6. **Webhook auth & delivery.** When `ticket_webhook` and `pr_webhook` event sources come online (likely v1.3), the daemon needs an HTTPS listener with auth (HMAC verification per provider) and a public URL (ngrok-style tunnel for local dev, real DNS for hosted). Out of scope here; flagged for the webhook ticket.

## Relationship to other docs

- [`templates.md`](./templates.md) — defines the parameterized template that consumers `init` to produce `.agent_team/`. The template ships an `instances.toml` with sensible defaults; consumers can override or extend at the repo level. The four-layer config resolution chain in `templates.md` extends to a five-layer chain when topology adds the `[instances.<name>.config]` layer (see "Layered config resolution chain" above).
- [`orchestrator.md`](./orchestrator.md) — the daemon owns trigger resolution and lifecycle. `POST /event` is the trigger entry point; `/dispatch` and `/message` are the implementation primitives the daemon uses to actuate matched triggers.
- (future) `channels.md` — channels become one event source: a publish to `#some-channel` is a `channel.message` event that subscribed instances' triggers can match against.

## What this doesn't change

- Agent definitions stay file-based and human-authored. Topology doesn't change what an agent *is*, only how it's wired up at the repo level.
- The bundled software-engineering team ships a default `instances.toml` (one `manager`, one `worker`, one `ticket-manager`, with sensible triggers). Consumers who don't need multiple instances see the same UX they have today.
- `agent-team run <agent>` keeps working for ad-hoc spawning. Topology is opt-in beyond that.
- The `assign-worker` skill stays as the user-facing wrapper for inter-agent dispatch (per Open Question #2). Implementation switches to the daemon API; surface is unchanged.
