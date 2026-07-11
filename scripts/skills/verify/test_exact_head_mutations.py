#!/usr/bin/env python3
"""Prove the preregistered GH-398-A exact-head mutants are test-sensitive."""

from __future__ import annotations

import argparse
import difflib
import importlib.util
import io
import sys
import tempfile
import unittest
from pathlib import Path
from typing import NamedTuple


REPO_ROOT = Path(__file__).resolve().parents[3]
VERIFY_PATH = REPO_ROOT / "template" / "skills" / "verify" / "scripts" / "verify.py"
TEST_PATH = REPO_ROOT / "scripts" / "skills" / "verify" / "test_verify.py"


class Mutant(NamedTuple):
    description: str
    old: str
    new: str
    test: str


MUTANTS = {
    "1": Mutant(
        "restore local-branch precedence over the authenticated PR head",
        """    if pr_identity is not None:
        if pr_query is None or pr_query.get(\"query_status\") != \"authenticated\":
""",
        """    if pr_identity is not None and not branch:
        if pr_query is None or pr_query.get(\"query_status\") != \"authenticated\":
""",
        "VerifyExactHeadTest.test_fixture_a_authoritative_head_wins_all_local_ref_shapes",
    ),
    "2": Mutant(
        "downgrade the evidence-write SHA equality assertion",
        """    if checkout_commit != evidence_commit or evidence_commit != query[\"head_commit\"]:
""",
        """    if False and (checkout_commit != evidence_commit or evidence_commit != query[\"head_commit\"]):
""",
        "VerifyExactHeadTest.test_fixture_b_head_advance_blocks_green_evidence_and_successful_completion",
    ),
    "4": Mutant(
        "reuse the cached resolution query when the fresh write query is unavailable",
        """    if query[\"query_status\"] != \"authenticated\":
        return make_exact_head_attestation(
            job_id,
            pipeline,
            pipeline_step,
            pr_identity,
            resolution_query,
            query,
            evidence_commit,
            \"unknown\",
            \"block_infra\",
            \"exact_head_unavailable\",
            \"infra_stop\",
        )
""",
        """    if query[\"query_status\"] != \"authenticated\":
        query = resolution_query
""",
        "VerifyExactHeadTest.test_unknown_write_query_fails_closed_instead_of_using_resolution_cache",
    ),
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mutant", choices=sorted(MUTANTS), action="append", help="Run only this mutant (repeatable).")
    parser.add_argument("--show-diff", action="store_true", help="Print the exact applied unified diff.")
    return parser.parse_args()


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


def run_mutant(mutant_id: str, mutant: Mutant, show_diff: bool) -> bool:
    source = VERIFY_PATH.read_text(encoding="utf-8")
    occurrences = source.count(mutant.old)
    if occurrences != 1:
        raise RuntimeError(f"mutant {mutant_id} expected one source match, found {occurrences}")
    mutated = source.replace(mutant.old, mutant.new, 1)
    if show_diff:
        print(
            "".join(
                difflib.unified_diff(
                    source.splitlines(keepends=True),
                    mutated.splitlines(keepends=True),
                    fromfile="verify.py",
                    tofile=f"verify.py (mutant {mutant_id})",
                )
            ),
            end="",
        )
    with tempfile.TemporaryDirectory() as temp:
        mutant_path = Path(temp) / f"verify_mutant_{mutant_id}.py"
        mutant_path.write_text(mutated, encoding="utf-8")
        verify_mutant = load_module(f"verify_mutant_{mutant_id}", mutant_path)
        test_module = load_module(f"test_verify_mutant_{mutant_id}", TEST_PATH)
        test_module.verify = verify_mutant
        suite = unittest.defaultTestLoader.loadTestsFromName(mutant.test, test_module)
        output = io.StringIO()
        result = unittest.TextTestRunner(stream=output, verbosity=2).run(suite)
    killed = not result.wasSuccessful()
    outcome = "KILLED" if killed else "SURVIVED"
    print(f"mutant {mutant_id}: {outcome} — {mutant.description}")
    print(f"  focused test: {mutant.test}")
    if not killed:
        print(output.getvalue(), end="")
    return killed


def main() -> int:
    args = parse_args()
    selected = args.mutant or sorted(MUTANTS)
    killed = [run_mutant(mutant_id, MUTANTS[mutant_id], args.show_diff) for mutant_id in selected]
    return 0 if all(killed) else 1


if __name__ == "__main__":
    raise SystemExit(main())
