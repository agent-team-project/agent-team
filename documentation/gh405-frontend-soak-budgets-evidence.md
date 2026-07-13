# GH-405 frontend soak-budget evidence

Work item: [GH-405](https://github.com/agent-team-project/kensho/issues/405)

Canonical base: `897ec6e4825ef3311be2b25ad0022cd5629b177f`

Tested implementation commit: `aa029f333beb9f5b98d40e3cfb2b908cf533d4bb`

## Budget contract

| Pipeline step | Timeout | Time budget |
| --- | ---: | ---: |
| `implement` | 3h | 3h |
| `verify` | 2h | 2h |

`TestFrontendProgramSoakBudgets` parses both the committed self-dogfood
topology and a rendered full-profile bundled template. The template-tree check
also binds the generated aggregate to the canonical frontend fragment.

## Load-bearing mutants

Each row temporarily restored one old declaration in
`.agent_team/instances.toml`, ran
`go test -count=1 ./internal/topology -run '^TestFrontendProgramSoakBudgets$'`,
and required the test to fail. The declarations were then restored before the
green control run.

| Mutant | Observed pair | Required pair | Result |
| --- | --- | --- | --- |
| implement `timeout = "60m"` | `1h0m0s/3h0m0s` | `3h0m0s/3h0m0s` | KILLED |
| implement `time_budget = "60m"` | `3h0m0s/1h0m0s` | `3h0m0s/3h0m0s` | KILLED |
| verify `timeout = "30m"` | `30m0s/2h0m0s` | `2h0m0s/2h0m0s` | KILLED |
| verify `time_budget = "30m"` | `2h0m0s/30m0s` | `2h0m0s/2h0m0s` | KILLED |

The restored green control passed.

## Ordinary gates

All of these passed before the timed soak:

```sh
test -z "$(gofmt -l .)"
python3 scripts/ci/validate_toml.py
python3 scripts/ci/validate_template_tree.py
go run ./cmd/agent-team --repo . topology validate
go vet ./...
go test -count=1 ./...
go build -o bin/agent-team ./cmd/agent-team
python3 scripts/ci/smoke_init.py bin/agent-team
```

## One-hour daemon-backed soak

The acceptance test ran against the tested implementation commit from
`2026-07-13T02:56:38Z` through `2026-07-13T03:56:40Z`:

```sh
AGENT_TEAM_TUI_SOAK=1 go test -timeout 70m -count=1 -v ./internal/tui -run '^TestOneHourSoak$'
```

The seeded live daemon exercised the fixed refresh cadence, filters,
navigation, and a real disconnect/reconnect cycle. Its final 30-minute heap
window, goroutine count, and file-descriptor count passed:

```text
SOAK EVIDENCE duration=1h0m0s scheduler=ProgramModel/tea.Tick cadence=5s schedules=719 current_ticks=713 stale_ticks=6 cadence_checks=719 refreshes=712 filters=237 navigations=712 routes=8 real_disconnect=true real_reconnect=true final_window=30m0s heap_slope_bytes_per_hour=-49790 retained_window_samples=5 retained_baseline_median=1296792 retained_final_median=1251000 retained_limit=1426471 goroutines=15->14 fds=11->11
--- PASS: TestOneHourSoak (3601.79s)
```

The raw timestamped log is retained in the durable instance state as
`gh405-one-hour-soak-aa029f33-full.log`; its SHA-256 is
`d1286f7b09fdd02e58bb199f2ed8d7cd8a2e316a3e73a00c2d5ad5a90186302a`.
An initial invocation without `-timeout 70m` reached Go's default 10-minute
test timeout and is not counted as soak evidence; the valid run above restarted
the full hour from zero.

## Scope boundary

This change raises only the frontend pipeline's implement and verify timeout
and time-budget pairs. It does not alter token budgets, daemon watchdog
behavior, inbox handling, or unrelated budget policy.
