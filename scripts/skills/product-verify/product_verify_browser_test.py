#!/usr/bin/env python3
from __future__ import annotations

import contextlib
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[3]
# These tests import the shipped template script directly; do not write bytecode
# into the embedded template tree.
sys.dont_write_bytecode = True
sys.path.insert(0, str(REPO_ROOT / "template" / "skills" / "product-verify" / "scripts"))
import product_verify_browser as verifier


def rendered_snapshot() -> dict[str, object]:
    return {
        "title": "agent-team daemon",
        "h1": "Daemon Dashboard",
        "tokenInputPresent": True,
        "tokenInputFilled": True,
        "connectionText": "Connected",
        "notice": "Daemon data loaded at 12:00:00.",
        "refreshState": "Every 15s",
        "metrics": {
            "instanceCount": "1",
            "runningCount": "1",
            "jobCount": "0",
            "activeJobCount": "0",
            "pipelineCount": "1",
            "budgetTeamCount": "0",
            "teamCount": "1",
        },
        "panels": {
            "instances": {"rows": 1, "text": "manager manager running -"},
            "jobs": {"rows": 1, "text": "No jobs recorded Dispatch a durable job to see state here."},
            "pipelines": {"rows": 1, "text": "ticket_to_pr agent.dispatch implement -> worker idle"},
            "budgets": {"rows": 1, "text": "No budgets configured Declare team budgets to track tokens."},
            "teams": {"rows": 1, "text": "platform 2 instances 1 pipeline 1 channel idle"},
        },
    }


class ProductVerifyBrowserTest(unittest.TestCase):
    def test_scheduled_entrypoint_does_not_write_bytecode(self) -> None:
        template_skill = REPO_ROOT / "template" / "skills" / "product-verify"
        with tempfile.TemporaryDirectory() as tmp:
            clean_target = Path(tmp)
            team_dir = clean_target / ".agent_team"
            installed_skill = team_dir / "skills" / "product-verify"
            shutil.copytree(template_skill, installed_skill)

            surfaces = {
                "template": template_skill,
                "installed": installed_skill,
            }
            for name, skill_dir in surfaces.items():
                with self.subTest(surface=name):
                    env = os.environ.copy()
                    env.pop("PYTHONDONTWRITEBYTECODE", None)
                    env.pop("PYTHONPYCACHEPREFIX", None)
                    env["AGENT_TEAM_ROOT"] = str(team_dir)
                    env["AGENT_TEAM_DAEMON_URL"] = ""
                    result = subprocess.run(
                        [
                            sys.executable,
                            str(skill_dir / "scripts" / "product_verify_browser.py"),
                            "--max-findings",
                            "5",
                        ],
                        cwd=clean_target,
                        env=env,
                        text=True,
                        capture_output=True,
                        check=False,
                    )

                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(json.loads(result.stdout)["status"], "skipped")
                    artifacts = sorted(
                        str(path.relative_to(skill_dir))
                        for path in skill_dir.rglob("*")
                        if path.name == "__pycache__" or path.suffix in {".pyc", ".pyo"}
                    )
                    self.assertEqual(artifacts, [])

    def test_dom_checks_accept_rendered_empty_states(self) -> None:
        checks = verifier.checks_for_dom_snapshot(rendered_snapshot())

        failed = [check for check in checks if not check["ok"]]

        self.assertEqual(failed, [])

    def test_dom_checks_report_error_panel(self) -> None:
        snapshot = rendered_snapshot()
        snapshot["panels"] = dict(snapshot["panels"])
        snapshot["panels"]["jobs"] = {
            "rows": 1,
            "text": "Jobs unavailable Unauthorized. Paste the token from .agent_team/daemon/operator.token and connect.",
        }

        checks = verifier.checks_for_dom_snapshot(snapshot)

        self.assertIn(
            {
                "name": "panel.jobs",
                "ok": False,
                "detail": {
                    "marker": "unavailable",
                    "text": "Jobs unavailable Unauthorized. Paste the token from .agent_team/daemon/operator.token and connect.",
                },
            },
            checks,
        )

    def test_browser_report_caps_findings_and_includes_screenshot(self) -> None:
        report = verifier.build_browser_report(
            checks=[
                {"name": "connection_state", "ok": False, "detail": {"connectionText": "Disconnected"}},
                {"name": "panel.jobs", "ok": False, "detail": {"text": "Jobs unavailable"}},
            ],
            browser_errors=[
                {"type": "console_error", "text": "boom"},
                {"type": "http_error", "url": "http://127.0.0.1:9000/v1/jobs", "status": 500},
            ],
            screenshot="/tmp/product-verify/broken-state.png",
            max_findings=2,
        )

        self.assertEqual(report["status"], "mismatch")
        self.assertEqual(report["summary"]["failed_checks"], 2)
        self.assertEqual(report["summary"]["browser_errors"], 2)
        self.assertEqual(report["summary"]["findings"], 2)
        self.assertTrue(report["summary"]["capped"])
        self.assertEqual(report["findings"][0]["category"], "bug")
        self.assertEqual(report["findings"][0]["screenshot"], "/tmp/product-verify/broken-state.png")

    def test_missing_http_addr_skips_cleanly(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            team_dir = Path(tmp) / ".agent_team"
            team_dir.mkdir()
            stdout = io.StringIO()

            with mock.patch.dict(os.environ, {"AGENT_TEAM_DAEMON_URL": ""}), contextlib.redirect_stdout(stdout):
                code = verifier.main(["--team-dir", str(team_dir)])

            self.assertEqual(code, 0)
            payload = json.loads(stdout.getvalue())
            self.assertEqual(payload["status"], "skipped")
            self.assertIn("HTTP address", payload["reason"])

    def test_missing_playwright_skips_cleanly(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            with mock.patch.object(verifier, "load_playwright", return_value=(None, None, None)):
                report = verifier.run_browser_check(
                    "http://127.0.0.1:9000",
                    "operator-token",
                    Path(tmp),
                    max_findings=5,
                    timeout_ms=1000,
                )

        self.assertEqual(report["status"], "skipped")
        self.assertIn("Playwright", report["reason"])


if __name__ == "__main__":
    unittest.main()
