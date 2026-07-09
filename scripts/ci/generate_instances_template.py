#!/usr/bin/env python3
"""Build the bundled instances topology template from ordered fragments."""

from __future__ import annotations

import argparse
import difflib
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
FRAGMENT_DIR = REPO_ROOT / "template" / "topology" / "instances.toml.tmpl.d"
OUTPUT_PATH = REPO_ROOT / "template" / "instances.toml.tmpl"


def collect_fragments() -> list[Path]:
    if not FRAGMENT_DIR.is_dir():
        raise RuntimeError(f"fragment directory not found: {FRAGMENT_DIR.relative_to(REPO_ROOT)}")
    fragments = sorted(path for path in FRAGMENT_DIR.glob("*.toml.tmpl") if path.is_file())
    if not fragments:
        raise RuntimeError(f"no topology fragments found under {FRAGMENT_DIR.relative_to(REPO_ROOT)}")
    return fragments


def build_template() -> str:
    chunks: list[str] = []
    for path in collect_fragments():
        body = path.read_text(encoding="utf-8")
        if not body.endswith("\n"):
            rel = path.relative_to(REPO_ROOT)
            raise RuntimeError(f"{rel}: fragment must end with a newline")
        chunks.append(body)
    return "".join(chunks)


def check_generated() -> int:
    expected = build_template()
    try:
        current = OUTPUT_PATH.read_text(encoding="utf-8")
    except FileNotFoundError:
        print(f"{OUTPUT_PATH.relative_to(REPO_ROOT)} is missing; run this script without --check", file=sys.stderr)
        return 1
    if current == expected:
        print(f"OK  {OUTPUT_PATH.relative_to(REPO_ROOT)} is generated from topology fragments")
        return 0

    rel = OUTPUT_PATH.relative_to(REPO_ROOT)
    print(f"{rel} is stale; run: python3 scripts/ci/generate_instances_template.py", file=sys.stderr)
    diff = difflib.unified_diff(
        current.splitlines(keepends=True),
        expected.splitlines(keepends=True),
        fromfile=f"{rel} (current)",
        tofile=f"{rel} (generated)",
    )
    sys.stderr.writelines(diff)
    return 1


def write_generated() -> int:
    OUTPUT_PATH.write_text(build_template(), encoding="utf-8")
    print(f"Wrote {OUTPUT_PATH.relative_to(REPO_ROOT)}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="fail if the generated template is stale")
    args = parser.parse_args()
    try:
        if args.check:
            return check_generated()
        return write_generated()
    except RuntimeError as exc:
        print(f"generate_instances_template.py: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
