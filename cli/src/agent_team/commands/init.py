"""`agent-team init` — vendor the template into a consumer repo."""

from __future__ import annotations

import shutil
from importlib import resources
from pathlib import Path
from typing import Annotated

import typer

TEAM_DIR_NAME = ".agent_team"

EMPTY_CONFIG = """\
# agent-team config — consumer-specific runtime values your skills read.
# This is the empty-template stub. Add sections as your skills require.
"""


def register(app: typer.Typer) -> None:
    @app.command(
        help=(
            "Vendor a starter team template into the current repo (creates .agent_team/). "
            "The default template ships a software-engineering team (ticket-manager, "
            "manager, worker, plus linear / pull-request / assign-worker skills). "
            "`--template empty` writes only the directory scaffold + a stub config.toml. "
            "Run `agent-team run` afterwards to launch Claude Code with the team registered."
        ),
    )
    def init(
        target: Annotated[Path, typer.Option(help="Target repo root.")] = Path.cwd(),
        force: Annotated[bool, typer.Option("--force", help="Overwrite existing .agent_team/ files (config.toml is never overwritten).")] = False,
        template: Annotated[str, typer.Option("--template", help="`default` (bundled software-eng team) or `empty` (scaffold only).")] = "default",
    ) -> None:
        if template not in ("default", "empty"):
            typer.echo(f"agent-team: --template must be `default` or `empty`, got {template!r}", err=True)
            raise typer.Exit(2)
        target = target.resolve()
        if not target.is_dir():
            typer.echo(f"agent-team: target is not a directory: {target}", err=True)
            raise typer.Exit(2)

        team_dir = target / TEAM_DIR_NAME
        template_root = _template_root()

        typer.echo(f"Vendoring team into {team_dir}")
        if template == "empty":
            _write_empty(team_dir)
        else:
            _copy_template(template_root, team_dir, force=force)
        _write_config(team_dir, template=template)

        typer.echo("")
        typer.echo("Done. Next steps:")
        typer.echo(f"  1. Edit {team_dir / 'config.toml'} (team_id, ticket_prefix, etc.).")
        typer.echo("  2. Add or edit agents under .agent_team/agents/<name>/ — each is a dir with agent.md, config.toml, optional skills/.")
        typer.echo("  3. Run `agent-team run` to launch Claude Code with your team registered.")
        typer.echo("  4. Run `agent-team doctor` to verify the layout is well-formed.")


def _template_root() -> Path:
    pkg_root = resources.files("agent_team")
    return Path(str(pkg_root / "template"))


def _copy_template(src: Path, dst: Path, *, force: bool) -> None:
    dst.mkdir(parents=True, exist_ok=True)
    for child in src.iterdir():
        if child.name == "config.toml.example":
            continue
        target = dst / child.name
        if target.exists() and not force:
            typer.echo(f"  skip {target.relative_to(dst.parent)} (already exists; --force to overwrite)")
            continue
        if child.is_dir():
            if target.exists():
                shutil.rmtree(target)
            shutil.copytree(child, target)
        else:
            shutil.copy2(child, target)
        typer.echo(f"  + {target.relative_to(dst.parent)}")


def _write_empty(dst: Path) -> None:
    (dst / "agents").mkdir(parents=True, exist_ok=True)
    (dst / "skills").mkdir(parents=True, exist_ok=True)
    typer.echo(f"  + {dst.name}/agents/")
    typer.echo(f"  + {dst.name}/skills/")


def _write_config(team_dir: Path, *, template: str) -> None:
    real_config = team_dir / "config.toml"
    if real_config.exists():
        typer.echo(f"  keep {real_config.relative_to(team_dir.parent)} (untouched)")
        return
    if template == "empty":
        real_config.write_text(EMPTY_CONFIG)
    else:
        src = _template_root() / "config.toml.example"
        shutil.copy2(src, team_dir / "config.toml.example")
        shutil.copy2(src, real_config)
    typer.echo(f"  + {real_config.relative_to(team_dir.parent)} (starter; edit before use)")
