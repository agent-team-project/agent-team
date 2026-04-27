"""agent-team — declare and launch Claude Code subagents and skills, vendored into any repo."""

from __future__ import annotations

import typer

from agent_team import __version__
from agent_team.commands import add as add_cmd
from agent_team.commands import doctor as doctor_cmd
from agent_team.commands import init as init_cmd
from agent_team.commands import run as run_cmd
from agent_team.commands import spawn as spawn_cmd

app = typer.Typer(
    name="agent-team",
    help="Declare and launch a custom set of Claude Code subagents and skills, vendored into any repo.",
    no_args_is_help=True,
    add_completion=False,
)


def _version_callback(value: bool) -> None:
    if value:
        typer.echo(f"agent-team {__version__}")
        raise typer.Exit()


@app.callback()
def _main(
    version: bool = typer.Option(
        False,
        "--version",
        help="Show version and exit.",
        is_eager=True,
        callback=_version_callback,
    ),
) -> None:
    """agent-team — declare and launch Claude Code subagents and skills."""


init_cmd.register(app)
run_cmd.register(app)
spawn_cmd.register(app)
doctor_cmd.register(app)
app.add_typer(add_cmd.app, name="add")


def main() -> None:
    app()


if __name__ == "__main__":
    main()
