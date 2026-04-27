"""`agent-team add` — scaffold new agents and skills."""

from __future__ import annotations

from pathlib import Path
from typing import Annotated, Optional

import typer

AGENT_TEMPLATE = """\
---
description: |
  TODO — what this agent does and when to invoke it. This becomes the agent's
  description in the --agents JSON Claude Code uses for routing.
---

# {title}

You are the `{name}` agent. TODO: describe your role, your scope, your
critical rules, and your workflow.

## Skills you have

This agent's skills are declared in ./config.toml under [skills].extra plus
any local skills under ./skills/.
"""

AGENT_CONFIG_TEMPLATE = """\
# Skills available to the `{name}` agent at runtime.
#
# Local skills under ./skills/ are auto-included.
# Pull in shared skills (under ../../skills/) by name, or anywhere by path:
[skills]
extra = []
# disable = []   # opt-out from local defaults if needed
"""

SKILL_TEMPLATE = """\
---
name: {name}
description: TODO — what this skill does. One sentence.
---

# {title}

TODO — write the skill's instructions, recipes, and bash patterns here.
"""


app = typer.Typer(
    help="Scaffold a new agent or skill (.agent_team/agents/<name>/ or .agent_team/skills/<name>/).",
    no_args_is_help=True,
)


def _check_kebab(value: str, what: str) -> None:
    if not value or not value.replace("-", "").isalnum() or value != value.lower():
        typer.echo(f"agent-team: {what} must be kebab-case lowercase alnum: {value!r}", err=True)
        raise typer.Exit(2)


@app.command(help="Scaffold a new agent at .agent_team/agents/<name>/.")
def agent(
    name: Annotated[str, typer.Argument(help="kebab-case identifier (e.g. `reviewer`).")],
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
) -> None:
    _check_kebab(name, "agent name")
    target = target.resolve()
    team_dir = target / ".agent_team"
    if not team_dir.is_dir():
        typer.echo(f"agent-team: {team_dir} not found — run `agent-team init` first.", err=True)
        raise typer.Exit(2)

    agent_dir = team_dir / "agents" / name
    if agent_dir.exists():
        typer.echo(f"agent-team: agent already exists: {agent_dir}", err=True)
        raise typer.Exit(1)
    agent_dir.mkdir(parents=True)
    title = name.replace("-", " ").title()
    (agent_dir / "agent.md").write_text(AGENT_TEMPLATE.format(name=name, title=title))
    (agent_dir / "config.toml").write_text(AGENT_CONFIG_TEMPLATE.format(name=name))
    typer.echo(f"  + {(agent_dir / 'agent.md').relative_to(target)}")
    typer.echo(f"  + {(agent_dir / 'config.toml').relative_to(target)}")
    typer.echo(f"\nAgent `{name}` scaffolded. Edit {agent_dir / 'agent.md'} to write its prompt.")


@app.command(help="Scaffold a new skill (shared by default; --agent scopes it under one agent).")
def skill(
    name: Annotated[str, typer.Argument(help="kebab-case identifier (e.g. `slack`).")],
    agent: Annotated[Optional[str], typer.Option("--agent", help="Scope under an existing agent (.agent_team/agents/<agent>/skills/<name>/).")] = None,
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
) -> None:
    _check_kebab(name, "skill name")
    target = target.resolve()
    team_dir = target / ".agent_team"
    if not team_dir.is_dir():
        typer.echo(f"agent-team: {team_dir} not found — run `agent-team init` first.", err=True)
        raise typer.Exit(2)

    if agent is not None:
        _check_kebab(agent, "agent name")
        agent_dir = team_dir / "agents" / agent
        if not agent_dir.is_dir():
            typer.echo(f"agent-team: agent dir not found: {agent_dir}", err=True)
            raise typer.Exit(2)
        skill_dir = agent_dir / "skills" / name
    else:
        skill_dir = team_dir / "skills" / name

    if skill_dir.exists():
        typer.echo(f"agent-team: skill already exists: {skill_dir}", err=True)
        raise typer.Exit(1)
    skill_dir.mkdir(parents=True)
    title = name.replace("-", " ").title()
    (skill_dir / "SKILL.md").write_text(SKILL_TEMPLATE.format(name=name, title=title))
    typer.echo(f"  + {(skill_dir / 'SKILL.md').relative_to(target)}")
    if agent is not None:
        typer.echo(f"\nSkill `{name}` scaffolded under agent `{agent}` (auto-included via that agent's local skills/).")
    else:
        typer.echo(f"\nSkill `{name}` scaffolded as a shared skill. Reference it from any agent's config.toml: [skills].extra = ['{name}'].")
