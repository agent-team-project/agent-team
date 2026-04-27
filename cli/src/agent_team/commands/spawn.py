"""`agent-team spawn` — instantiate one agent as a Claude Code session.

Where `agent-team run` registers every agent as a subagent for orchestrator-style
work, `spawn <agent>` makes the launched Claude Code session BE the named agent:
the agent's prompt becomes part of the system prompt, the agent's skills are
loaded, and a kickoff preamble names the instance and its state directory.

All agents are still registered as subagents in the spawned session so the
named agent can dispatch others via the Task tool (e.g. a spawned `manager`
can still call `assign-worker`).

Per-instance state (created on demand): `.agent_team/state/<instance-name>/`.
"""

from __future__ import annotations

import json
import os
import subprocess
import tempfile
from pathlib import Path
from typing import Annotated, Optional

import typer

from agent_team.commands.run import AgentLoadError, _union_skills, load_agent

TEAM_DIR_NAME = ".agent_team"


def register(app: typer.Typer) -> None:
    @app.command(
        name="spawn",
        help=(
            "Instantiate one agent as a Claude Code session. The launched session IS the "
            "named agent (its prompt becomes the system prompt). All other agents are still "
            "registered as subagents so the spawned agent can dispatch them. Pass `--name` "
            "to give the instance a unique identifier (state dir: .agent_team/state/<name>/). "
            "Forward extra args to claude after `--`."
        ),
        context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
    )
    def spawn(
        ctx: typer.Context,
        agent_name: Annotated[str, typer.Argument(help="Agent name — directory under .agent_team/agents/.")],
        name: Annotated[Optional[str], typer.Option("--name", "-n",
            help="Instance name (defaults to the agent name). State dir: .agent_team/state/<name>/.")] = None,
        prompt: Annotated[Optional[str], typer.Option("--prompt", "-p",
            help="Kickoff message. With this, claude runs in one-shot mode; without, interactive.")] = None,
        target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
    ) -> None:
        target = target.resolve()
        team_dir = target / TEAM_DIR_NAME
        if not team_dir.is_dir():
            typer.echo(f"agent-team: {team_dir} not found — run `agent-team init` first.", err=True)
            raise typer.Exit(2)

        agents_dir = team_dir / "agents"
        chosen_dir = agents_dir / agent_name
        if not chosen_dir.is_dir():
            typer.echo(f"agent-team: agent not found: {chosen_dir}", err=True)
            raise typer.Exit(2)

        try:
            agents = [load_agent(d, team_dir)
                      for d in sorted(p for p in agents_dir.iterdir() if p.is_dir())]
        except AgentLoadError as e:
            typer.echo(f"agent-team: {e}", err=True)
            raise typer.Exit(1)

        chosen = next((a for a in agents if a.name == agent_name), None)
        if chosen is None:
            typer.echo(f"agent-team: failed to load agent `{agent_name}`", err=True)
            raise typer.Exit(1)

        try:
            skill_paths = _union_skills(agents)
        except AgentLoadError as e:
            typer.echo(f"agent-team: {e}", err=True)
            raise typer.Exit(1)

        instance = name or agent_name
        state_dir = team_dir / "state" / instance
        state_dir.mkdir(parents=True, exist_ok=True)

        agents_json = {a.name: {"description": a.description, "prompt": a.prompt} for a in agents}

        forwarded = list(ctx.args)
        if forwarded and forwarded[0] == "--":
            forwarded = forwarded[1:]

        with tempfile.TemporaryDirectory(prefix="agent-team-spawn-") as tmpdir_str:
            tmpdir = Path(tmpdir_str)

            skills_root = tmpdir / ".claude" / "skills"
            skills_root.mkdir(parents=True)
            for sname, spath in skill_paths.items():
                (skills_root / sname).symlink_to(spath)

            kickoff = (
                f"You are the `{instance}` instance of the `{agent_name}` agent.\n"
                f"Your state dir is `{state_dir.relative_to(target)}` "
                f"(absolute: `{state_dir}`).\n\n"
                f"--- agent prompt ---\n\n"
                f"{chosen.prompt}"
            )
            prompt_file = tmpdir / "system_prompt.md"
            prompt_file.write_text(kickoff)

            env = {
                **os.environ,
                "AGENT_TEAM_ROOT": str(team_dir),
                "AGENT_TEAM_INSTANCE": instance,
                "AGENT_TEAM_STATE_DIR": str(state_dir),
            }

            cmd = [
                "claude",
                "--agents", json.dumps(agents_json, separators=(",", ":")),
                "--add-dir", str(tmpdir),
                "--append-system-prompt-file", str(prompt_file),
            ]
            if prompt:
                cmd += ["-p", prompt]
            cmd.extend(forwarded)

            try:
                rc = subprocess.run(cmd, env=env, cwd=str(target)).returncode
            except FileNotFoundError:
                typer.echo("agent-team: `claude` CLI not found in PATH. Install Claude Code first.", err=True)
                raise typer.Exit(127)
            if rc != 0:
                raise typer.Exit(rc)
