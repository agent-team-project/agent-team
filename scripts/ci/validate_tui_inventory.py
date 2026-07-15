#!/usr/bin/env python3
"""Fail closed when the TUI cutover source inventory no longer matches Git."""

from __future__ import annotations

import argparse
import hashlib
import re
import subprocess
import sys
from dataclasses import dataclass, replace
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MANIFEST = Path("documentation/tui/parity.yaml")
REQUIRED_SCOPE = (
    "internal/daemon/ui",
    "internal/daemon/ui.go",
    "internal/daemon/http.go",
    "internal/daemon/http_auth.go",
)
STALE_DIGEST_MUTANT = (
    "8c844f7a6d93bc762c7d1ce0a755916376586f8a981330ede928e707b9714496"
)
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")


@dataclass(frozen=True)
class SourceInventory:
    base_commit: str
    algorithm: str
    serialization: str
    scope: tuple[str, ...]
    digest: str


def yaml_scalar(raw: str) -> str:
    value = raw.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {'"', "'"}:
        return value[1:-1]
    return value


def parse_source_inventory(path: Path) -> SourceInventory:
    """Parse the small fail-closed inventory subset without a YAML dependency."""
    lines = path.read_text(encoding="utf-8").splitlines()
    try:
        source_start = lines.index("source_inventory:") + 1
    except ValueError as error:
        raise ValueError("missing source_inventory mapping") from error

    source_end = len(lines)
    for index in range(source_start, len(lines)):
        line = lines[index]
        if line.strip() and not line.startswith(" "):
            source_end = index
            break
    source_lines = lines[source_start:source_end]

    base_commit = ""
    digest_start = -1
    for index, line in enumerate(source_lines):
        if line.startswith("  base_commit:"):
            base_commit = yaml_scalar(line.split(":", 1)[1])
        if line == "  digest:":
            digest_start = index + 1

    if digest_start < 0:
        raise ValueError("missing source_inventory.digest mapping")

    digest_end = len(source_lines)
    for index in range(digest_start, len(source_lines)):
        line = source_lines[index]
        if line.strip() and not line.startswith("    "):
            digest_end = index
            break
    digest_lines = source_lines[digest_start:digest_end]

    algorithm = ""
    serialization = ""
    expected_digest = ""
    scope: list[str] = []
    reading_scope = False
    for line in digest_lines:
        stripped = line.strip()
        indent = len(line) - len(line.lstrip(" "))
        if indent == 4:
            reading_scope = stripped == "scope:"
            if stripped.startswith("algorithm:"):
                algorithm = yaml_scalar(stripped.split(":", 1)[1])
            elif stripped.startswith("serialization:"):
                serialization = yaml_scalar(stripped.split(":", 1)[1])
            elif stripped.startswith("value:"):
                expected_digest = yaml_scalar(stripped.split(":", 1)[1])
            continue
        if reading_scope and indent == 6 and stripped.startswith("- "):
            scope.append(yaml_scalar(stripped[2:]))

    return SourceInventory(
        base_commit=base_commit,
        algorithm=algorithm,
        serialization=serialization,
        scope=tuple(scope),
        digest=expected_digest,
    )


def git(repo_root: Path, *args: str) -> subprocess.CompletedProcess[bytes]:
    return subprocess.run(
        ["git", *args],
        cwd=repo_root,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )


def calculate_digest(repo_root: Path, inventory: SourceInventory) -> tuple[str, list[str]]:
    failures: list[str] = []
    commit = git(repo_root, "rev-parse", "--verify", f"{inventory.base_commit}^{{commit}}")
    if commit.returncode != 0:
        detail = commit.stderr.decode("utf-8", errors="replace").strip()
        failures.append(
            f"base commit {inventory.base_commit!r} is unavailable; fetch the audited history ({detail})"
        )
        return "", failures

    tree = git(
        repo_root,
        "ls-tree",
        "-r",
        "-z",
        "--full-tree",
        inventory.base_commit,
        "--",
        *inventory.scope,
    )
    if tree.returncode != 0:
        detail = tree.stderr.decode("utf-8", errors="replace").strip()
        failures.append(f"git ls-tree failed: {detail}")
        return "", failures
    return hashlib.sha256(tree.stdout).hexdigest(), failures


def validate(repo_root: Path, inventory: SourceInventory) -> tuple[list[str], str]:
    failures: list[str] = []
    if not COMMIT_RE.fullmatch(inventory.base_commit):
        failures.append(
            "source_inventory.base_commit must be a full lowercase "
            "40-character commit SHA"
        )
    if inventory.algorithm != "sha256":
        failures.append("source_inventory.digest.algorithm must remain sha256")
    if inventory.serialization != "git-ls-tree-r-z-full-tree":
        failures.append(
            "source_inventory.digest.serialization must remain git-ls-tree-r-z-full-tree"
        )
    if inventory.scope != REQUIRED_SCOPE:
        failures.append(
            "source_inventory.digest.scope must remain the ordered fail-closed scope: "
            + ", ".join(REQUIRED_SCOPE)
        )
    if not SHA256_RE.fullmatch(inventory.digest):
        failures.append("source_inventory.digest.value must be a lowercase SHA-256 digest")
    if failures:
        return failures, ""

    actual_digest, git_failures = calculate_digest(repo_root, inventory)
    failures.extend(git_failures)
    if not git_failures and actual_digest != inventory.digest:
        failures.append(
            "source inventory digest mismatch at "
            f"{inventory.base_commit}: expected {inventory.digest}, calculated {actual_digest}"
        )
    return failures, actual_digest


def resolve_manifest(repo_root: Path, manifest: Path) -> Path:
    return manifest if manifest.is_absolute() else repo_root / manifest


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=REPO_ROOT)
    parser.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    parser.add_argument(
        "--mutant",
        action="store_true",
        help="prove the validator rejects the inventory's previous stale digest",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    repo_root = args.repo_root.resolve()
    manifest = resolve_manifest(repo_root, args.manifest).resolve()
    try:
        inventory = parse_source_inventory(manifest)
    except (OSError, ValueError) as error:
        print(f"FAIL {manifest}: {error}", file=sys.stderr)
        return 1

    checked = replace(inventory, digest=STALE_DIGEST_MUTANT) if args.mutant else inventory
    failures, actual_digest = validate(repo_root, checked)
    if args.mutant:
        expected_failure = any(
            "source inventory digest mismatch" in failure
            and STALE_DIGEST_MUTANT in failure
            for failure in failures
        )
        if not expected_failure or len(failures) != 1:
            print(
                "FAIL stale-digest mutant did not produce exactly the expected "
                "inventory mismatch",
                file=sys.stderr,
            )
            for failure in failures:
                print(f"  - {failure}", file=sys.stderr)
            return 1
        print(
            "OK  stale-digest mutant rejected: "
            f"{STALE_DIGEST_MUTANT} != {actual_digest}"
        )
        return 0

    if failures:
        print(f"TUI source inventory validation failed ({manifest}):", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1

    relative_manifest = (
        manifest.relative_to(repo_root)
        if manifest.is_relative_to(repo_root)
        else manifest
    )
    print(
        f"OK  {relative_manifest}: {inventory.base_commit} -> {actual_digest} "
        f"({len(inventory.scope)} scoped paths)"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
