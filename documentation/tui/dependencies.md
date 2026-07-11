# Terminal UI dependency pins

GH-384 resolves the ADR-001 Charm stack to one Go 1.22-compatible v1 line:

| Module | Exact pin | Role |
| --- | --- | --- |
| `github.com/charmbracelet/bubbletea` | `v1.3.4` | program loop and terminal integration |
| `github.com/charmbracelet/lipgloss` | `v1.1.0` | deterministic view styling |
| `github.com/charmbracelet/bubbles` | `v0.20.0` | keyboard and text-input adapters |
| `github.com/charmbracelet/x/exp/teatest` | `v0.0.0-20241028122716-59f28b971972` | Bubble Tea integration harness |

Bubble Tea `v1.3.4`, Lip Gloss `v1.1.0`, and Bubbles `v0.20.0` are tagged
releases in the same compatible major line and retain Go 1.22 compatibility.
`teatest` has no tagged module release; its reviewed pseudo-version is pinned to
the last Go 1.19-compatible commit that tracks Bubble Tea v1, rather than an
implicit `@latest` selection. The pin is exact and covered by a dependency test.

Permanent tests reject direct Charm imports outside `internal/tui` and reject
any `github.com/charmbracelet/` package in `agent-teamd`'s transitive dependency
closure. The TUI command layer also has a structural read-only check that rejects
mutating daemon-client method calls.
