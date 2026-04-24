# squirtle-squad

A Claude Code plugin that packages a reusable "software engineering team" — agents and skills that, installed into any repo, let a human drive a swarm of Claude Code workers to implement Linear tickets end-to-end.

**Status**: pre-v1. Under active development. See [`documentation/`](./documentation) for the product strategy, architecture, roadmap, and open questions.

## Install

```shell
/plugin marketplace add jamesaud/squirtle-squad
/plugin install squirtle-squad@squirtle-squad
```

Private repo; requires GitHub access to `jamesaud/squirtle-squad`.

## Local development

The repo dogfoods itself — Claude Code running inside this checkout uses the plugin from its own source tree via a local-path marketplace:

```shell
/plugin marketplace add /Users/jamesaud/projects/squirtle-squad
/plugin install squirtle-squad@squirtle-squad

# After editing plugin source:
/plugin marketplace update squirtle-squad
/reload-plugins
```

Full dev loop details: [`documentation/notes/plugin-schema.md`](./documentation/notes/plugin-schema.md).

## Repo layout

```
.claude-plugin/marketplace.json          # marketplace manifest
plugins/squirtle-squad/                  # the plugin
  .claude-plugin/plugin.json
  agents/                                # auto-discovered
  skills/                                # auto-discovered
documentation/                           # strategy, architecture, notes
```

## What's here today

The v1 plugin ships a `ticket-manager` agent, a `worker` agent, and three skills — `linear`, `pull-request`, and `assign-worker`. V1's remaining work is self-dogfooding: closing a real SQU ticket via a worker-opened PR against this repo. See [`documentation/roadmap.md`](./documentation/roadmap.md) for milestone status.

## Docs

- [`CLAUDE.md`](./CLAUDE.md) — contributor-facing orientation (repo layout, dev loop, config conventions, contribution rules).
- [`documentation/vision.md`](./documentation/vision.md) — what this is, who it's for, principles.
- [`documentation/architecture.md`](./documentation/architecture.md) — plugin shape, customization model, credentials.
- [`documentation/roadmap.md`](./documentation/roadmap.md) — milestones M0 → M6 + parking lot.
- [`documentation/open-questions.md`](./documentation/open-questions.md) — open research items.
- [`documentation/notes/plugin-schema.md`](./documentation/notes/plugin-schema.md) — Claude Code plugin schema reference with worked examples.
