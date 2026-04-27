#!/usr/bin/env python3
"""End-to-end smoke test for the installed agent-team CLI.

Assumes `agent-team` is on PATH (CI step `pip install ./cli` runs first).
Exercises:
  - `init` against a tmp dir → expected per-agent dirs and shared skills exist
  - `add agent <name>` → scaffolds new agent dir
  - `add skill <name>` → scaffolds shared skill
  - `add skill <name> --agent <agent>` → scaffolds agent-private skill
  - `doctor` fails when Linear keys empty, passes once filled in
"""

from __future__ import annotations

import subprocess
import sys
import tempfile
import tomllib
from pathlib import Path

EXPECTED_AFTER_INIT = [
    ".agent_team/config.toml",
    ".agent_team/config.toml.example",
    ".agent_team/agents/ticket-manager/agent.md",
    ".agent_team/agents/ticket-manager/config.toml",
    ".agent_team/agents/manager/agent.md",
    ".agent_team/agents/manager/config.toml",
    ".agent_team/agents/manager/skills/assign-worker/SKILL.md",
    ".agent_team/agents/worker/agent.md",
    ".agent_team/agents/worker/config.toml",
    ".agent_team/skills/linear/SKILL.md",
    ".agent_team/skills/linear/scripts/linear-graphql.sh",
    ".agent_team/skills/pull-request/SKILL.md",
]


def main() -> int:
    problems: list[str] = []
    with tempfile.TemporaryDirectory() as tmp:
        target = Path(tmp)

        run(["agent-team", "init", "--target", str(target)])
        for rel in EXPECTED_AFTER_INIT:
            if not (target / rel).exists():
                problems.append(f"missing after init: {rel}")

        try:
            tomllib.loads((target / ".agent_team" / "config.toml").read_text())
        except Exception as e:  # noqa: BLE001
            problems.append(f"config.toml not valid TOML: {e}")

        run(["agent-team", "add", "agent", "smoke-agent", "--target", str(target)])
        if not (target / ".agent_team/agents/smoke-agent/agent.md").exists():
            problems.append("add agent didn't scaffold agent.md")
        if not (target / ".agent_team/agents/smoke-agent/config.toml").exists():
            problems.append("add agent didn't scaffold config.toml")

        run(["agent-team", "add", "skill", "smoke-skill", "--target", str(target)])
        if not (target / ".agent_team/skills/smoke-skill/SKILL.md").exists():
            problems.append("add skill didn't scaffold SKILL.md")

        run(["agent-team", "add", "skill", "private-skill",
             "--agent", "smoke-agent", "--target", str(target)])
        if not (target / ".agent_team/agents/smoke-agent/skills/private-skill/SKILL.md").exists():
            problems.append("add skill --agent didn't scaffold SKILL.md")

        rc = subprocess.run(
            ["agent-team", "doctor", "--target", str(target)],
            capture_output=True, text=True,
        ).returncode
        if rc == 0:
            problems.append("doctor passed with empty Linear keys (should have failed)")

        cfg_path = target / ".agent_team" / "config.toml"
        cfg = cfg_path.read_text()
        cfg = cfg.replace('team_id       = ""', 'team_id       = "smoke-team"')
        cfg = cfg.replace('ticket_prefix = ""', 'ticket_prefix = "SMK"')
        cfg_path.write_text(cfg)
        rc = subprocess.run(
            ["agent-team", "doctor", "--target", str(target)],
            capture_output=True, text=True,
        ).returncode
        if rc != 0:
            problems.append("doctor failed with valid Linear keys")

    if problems:
        print("smoke_init failed:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        return 1
    print("OK  agent-team init + add + doctor")
    return 0


def run(cmd: list[str]) -> None:
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        print(f"command failed: {' '.join(cmd)}", file=sys.stderr)
        print(r.stdout, file=sys.stderr)
        print(r.stderr, file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    sys.exit(main())
