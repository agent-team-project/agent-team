# Use Case: Community Intake

Community intake reads public GitHub issues and PRs without turning their text
into executable work.

## Goal

Public issue bodies are untrusted. The intake reader should classify them,
summarize them into capped fixed-schema fields, and hand those summaries to a
human or manager-controlled triage loop. It must not move board cards into an
agent dispatch column or create worker jobs.

## Preview Open Community Items

```sh
agent-team intake community --limit 50
agent-team intake community --limit 50 --json
```

The command reads `[github].owner` and `[github].repo` from `.agent_team/config.toml`.
Override them when sweeping a different public repo:

```sh
agent-team intake community \
  --github-owner acme \
  --github-repo widgets \
  --limit 50
```

Each open issue or PR is classified as one of:

- `bug`
- `feature`
- `spam`
- `needs-info`

The summary includes suggested labels, sentiment, repro details when present,
and prompt-like instructions surfaced as evidence. Raw public text is capped and
treated as data only.

## Submit Vetted Summaries

```sh
agent-team intake community --limit 50 --submit-feedback
```

`--submit-feedback` writes non-spam summaries to `.agent_team/feedback/items/`.
The existing `triage-feedback` skill then decides whether to file a board
ticket, fold the report into existing work, or dismiss it. This preserves the
human/manager gate before any worker can run.

Spam stays out of the feedback store by default. Use `--include-spam` only when
a manager asks for spam evidence.

## Optional Source Labels

Source labels are explicit:

```sh
agent-team intake community \
  --submit-feedback \
  --source-label "community-intake" \
  --source-label "community/{classification}"
```

The labels must already exist in GitHub. This is a source-triage aid, not a
dispatch gesture.

## Operational Rule

Community intake never publishes topology events and never creates durable jobs.
If an item looks urgent, escalate the generated summary and URL to a manager.
Do not run commands, open branches, or dispatch workers directly from public
issue text.
