"""`agent-team run` — launch Claude Code with the local .agent_team/ registered.

Reads .agent_team/agents/*/agent.md, parses frontmatter (description) + body
(prompt), resolves each agent's skill set from <agent_dir>/skills/ and the
[skills].extra list in <agent_dir>/config.toml, builds a tmpdir of symlinks
satisfying Claude Code's --add-dir skill-discovery layout, and exec's
`claude --agents '<json>' --add-dir <tmpdir> <forwarded args>`.

Subagents are session-scoped — they live only for the duration of the spawned
claude process; nothing is written into .claude/agents/.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import tomllib
from pathlib import Path
from typing import Annotated

import typer

TEAM_DIR_NAME = ".agent_team"


def register(app: typer.Typer) -> None:
    @app.command(
        name="run",
        help=(
            "Launch Claude Code with the local .agent_team/ registered. "
            "Forward extra args to claude after `--`, e.g. "
            "`agent-team run -- -p 'work on SQU-14'`."
        ),
        context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
    )
    def run(
        ctx: typer.Context,
        target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
    ) -> None:
        target = target.resolve()
        team_dir = target / TEAM_DIR_NAME
        if not team_dir.is_dir():
            typer.echo(f"agent-team: {team_dir} not found — run `agent-team init` first.", err=True)
            raise typer.Exit(2)

        agents_dir = team_dir / "agents"
        if not agents_dir.is_dir():
            typer.echo(f"agent-team: {agents_dir} not found", err=True)
            raise typer.Exit(2)

        try:
            agents = [load_agent(d, team_dir)
                      for d in sorted(p for p in agents_dir.iterdir() if p.is_dir())]
        except AgentLoadError as e:
            typer.echo(f"agent-team: {e}", err=True)
            raise typer.Exit(1)
        if not agents:
            typer.echo(f"agent-team: no agents found in {agents_dir}", err=True)
            raise typer.Exit(2)

        try:
            skill_paths = _union_skills(agents)
        except AgentLoadError as e:
            typer.echo(f"agent-team: {e}", err=True)
            raise typer.Exit(1)

        agents_json = {a.name: {"description": a.description, "prompt": a.prompt} for a in agents}

        forwarded = list(ctx.args)
        if forwarded and forwarded[0] == "--":
            forwarded = forwarded[1:]

        with tempfile.TemporaryDirectory(prefix="agent-team-") as tmpdir_str:
            tmpdir = Path(tmpdir_str)
            skills_root = tmpdir / ".claude" / "skills"
            skills_root.mkdir(parents=True)
            for sname, spath in skill_paths.items():
                (skills_root / sname).symlink_to(spath)

            env = {**os.environ, "AGENT_TEAM_ROOT": str(team_dir)}

            cmd = [
                "claude",
                "--agents", json.dumps(agents_json, separators=(",", ":")),
                "--add-dir", str(tmpdir),
                *forwarded,
            ]

            try:
                rc = subprocess.run(cmd, env=env, cwd=str(target)).returncode
            except FileNotFoundError:
                typer.echo("agent-team: `claude` CLI not found in PATH. Install Claude Code first.", err=True)
                raise typer.Exit(127)
            if rc != 0:
                raise typer.Exit(rc)


class AgentLoadError(RuntimeError):
    pass


class Agent:
    __slots__ = ("name", "description", "prompt", "skills")

    def __init__(self, name: str, description: str, prompt: str, skills: dict[str, Path]):
        self.name = name
        self.description = description
        self.prompt = prompt
        self.skills = skills


def load_agent(agent_dir: Path, team_dir: Path) -> Agent:
    md_path = agent_dir / "agent.md"
    if not md_path.is_file():
        raise AgentLoadError(f"{md_path} missing — every agent dir needs an agent.md")
    fm, body = parse_frontmatter(md_path.read_text())
    description = fm.get("description", "").strip()
    if not description:
        raise AgentLoadError(f"{md_path} has no `description` in frontmatter")
    skills = resolve_skills(agent_dir, team_dir)
    return Agent(name=agent_dir.name, description=description, prompt=body, skills=skills)


def resolve_skills(agent_dir: Path, team_dir: Path) -> dict[str, Path]:
    """{skill_name: absolute_path}. Local skills auto-included; `extra` pulls in shared/path-referenced."""
    skills: dict[str, Path] = {}

    local_root = agent_dir / "skills"
    if local_root.is_dir():
        for child in sorted(local_root.iterdir()):
            if child.is_dir() and (child / "SKILL.md").is_file():
                skills[child.name] = child.resolve()

    cfg_path = agent_dir / "config.toml"
    extra: list[str] = []
    disable: list[str] = []
    if cfg_path.is_file():
        cfg = tomllib.loads(cfg_path.read_text())
        skills_cfg = cfg.get("skills", {})
        extra = list(skills_cfg.get("extra", []))
        disable = list(skills_cfg.get("disable", []))

    shared_root = team_dir / "skills"
    for spec in extra:
        if "/" in spec or spec.startswith("."):
            path = (agent_dir / spec).resolve()
        else:
            path = (shared_root / spec).resolve()
        if not path.is_dir() or not (path / "SKILL.md").is_file():
            raise AgentLoadError(
                f"{agent_dir.name}: skill `{spec}` not found at {path} (no SKILL.md)"
            )
        name = path.name
        if name in skills and skills[name] != path:
            raise AgentLoadError(
                f"{agent_dir.name}: skill name `{name}` is already a local skill at "
                f"{skills[name]}; can't also import a different `{spec}`"
            )
        skills[name] = path

    for name in disable:
        skills.pop(name, None)

    return skills


def _union_skills(agents: list[Agent]) -> dict[str, Path]:
    """Combine all agents' skills into a session-wide set, erroring on name collision across agents."""
    union: dict[str, Path] = {}
    for agent in agents:
        for name, path in agent.skills.items():
            existing = union.get(name)
            if existing is not None and existing != path:
                raise AgentLoadError(
                    f"skill name `{name}` resolves to two different paths "
                    f"({existing} vs {path}); rename one."
                )
            union[name] = path
    return union


def parse_frontmatter(text: str) -> tuple[dict[str, str], str]:
    """Split a markdown file with `---`-delimited YAML frontmatter into (fm_dict, body).

    Supports the subset of YAML actually used in agent frontmatter:
      key: scalar
      key: |
        block scalar line 1
        block scalar line 2
    Lists and nested mappings are skipped — they aren't surfaced into the
    --agents JSON anyway (we only need `description`).
    """
    if not text.startswith("---\n"):
        return {}, text
    end_idx = text.find("\n---\n", 4)
    if end_idx == -1:
        if text.endswith("\n---"):
            return _parse_yaml_subset(text[4:-4]), ""
        return {}, text
    fm_text = text[4:end_idx]
    body = text[end_idx + 5:]
    return _parse_yaml_subset(fm_text), body


def _parse_yaml_subset(text: str) -> dict[str, str]:
    result: dict[str, str] = {}
    lines = text.split("\n")
    i = 0
    while i < len(lines):
        line = lines[i]
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            i += 1
            continue
        if line.startswith((" ", "\t", "-")):
            i += 1
            continue
        if ":" not in line:
            i += 1
            continue
        key, _, val = line.partition(":")
        key = key.strip()
        val = val.strip()
        if val == "|":
            i += 1
            block_lines: list[str] = []
            base_indent: int | None = None
            while i < len(lines):
                ln = lines[i]
                if not ln.strip():
                    block_lines.append("")
                    i += 1
                    continue
                indent = len(ln) - len(ln.lstrip(" "))
                if base_indent is None:
                    if indent == 0:
                        break
                    base_indent = indent
                if indent < base_indent:
                    break
                block_lines.append(ln[base_indent:])
                i += 1
            result[key] = "\n".join(block_lines).rstrip("\n")
            continue
        if (val.startswith('"') and val.endswith('"')) or (val.startswith("'") and val.endswith("'")):
            val = val[1:-1]
        result[key] = val
        i += 1
    return result
