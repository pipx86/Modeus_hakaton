#!/usr/bin/env python3
"""Stdlib tests for the demo-day tooling (no network, no live stack needed).

Run from the repository root:
    python scripts/test_demo_tools.py
    python -m unittest discover -s scripts -p "test_*.py"
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import os
import sys
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest import mock

# Keep the repository clean: importing the tools must not leave
# scripts/__pycache__ behind.
sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))

import demo_data as data  # noqa: E402
import demo_preflight as preflight  # noqa: E402


def _verify_hs256(token: str, secret: str) -> dict:
    """Independently verify an HS256 JWT and return its claims."""

    header_b64, payload_b64, signature_b64 = token.split(".")
    signing_input = f"{header_b64}.{payload_b64}".encode("ascii")
    expected = hmac.new(secret.encode("utf-8"), signing_input, hashlib.sha256).digest()
    actual = base64.urlsafe_b64decode(signature_b64 + "=" * (-len(signature_b64) % 4))
    assert hmac.compare_digest(expected, actual), "signature mismatch"
    return json.loads(base64.urlsafe_b64decode(payload_b64 + "=" * (-len(payload_b64) % 4)))


def _row(run_id: str, status: str, started_days_ago: float = 0.0, nodes: int | None = None) -> "data.RunRow":
    started = datetime.now(timezone.utc) - timedelta(days=started_days_ago)
    return data.RunRow(
        id=run_id,
        status=status,
        profession="Data Analyst",
        stage=status,
        started_at=started.isoformat(),
        finished_at="",
        nodes=nodes,
    )


class FakeClient:
    """Records DELETE calls and answers with a configurable status."""

    def __init__(self, status: int = 204):
        self.status = status
        self.deleted: list[str] = []

    def delete(self, path: str):
        self.deleted.append(path.rsplit("/", 1)[-1])
        return preflight.HttpResponse(status=self.status, url=path, body=b"")


class TokenTests(unittest.TestCase):
    def test_minted_token_is_valid_hs256(self):
        token = preflight.mint_token("demo-secret", subject="demo-preflight", role="admin", ttl_seconds=600)
        claims = _verify_hs256(token, "demo-secret")
        self.assertEqual(claims["sub"], "demo-preflight")
        self.assertEqual(claims["role"], "admin")
        self.assertGreater(claims["exp"], claims["iat"])

    def test_minted_token_fails_with_another_secret(self):
        token = preflight.mint_token("demo-secret")
        with self.assertRaises(AssertionError):
            _verify_hs256(token, "another-secret")

    def test_resolve_token_prefers_explicit_token(self):
        token, source = preflight.resolve_token("explicit-token", {})
        self.assertEqual(token, "explicit-token")
        self.assertEqual(source, "--token")

    def test_resolve_token_uses_demo_token_env(self):
        with mock.patch.dict(os.environ, {"DEMO_TOKEN": "env-token"}, clear=True):
            token, source = preflight.resolve_token(None, {})
        self.assertEqual(token, "env-token")
        self.assertEqual(source, "$DEMO_TOKEN")

    def test_resolve_token_mints_from_dotenv_secret(self):
        # clear the environment so a developer's exported JWT_SECRET cannot win
        with mock.patch.dict(os.environ, {}, clear=True):
            token, source = preflight.resolve_token(None, {"JWT_SECRET": "file-secret"})
        self.assertEqual(source, "JWT_SECRET")
        self.assertEqual(_verify_hs256(token, "file-secret")["role"], "admin")

    def test_resolve_token_without_any_secret_is_actionable(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            with self.assertRaises(preflight.DemoAPIError) as ctx:
                preflight.resolve_token(None, {})
        self.assertIn("--token", str(ctx.exception))
        self.assertIn("JWT_SECRET", str(ctx.exception))


class EnvParsingTests(unittest.TestCase):
    def test_parse_dotenv_text_handles_quotes_comments_and_export(self):
        parsed = preflight.parse_dotenv_text(
            "\n".join(
                [
                    "# comment",
                    "JWT_SECRET=abc123",
                    'LLM_BASE_URL="https://example.test/v1"',
                    "export LLM_API_KEY='sk-test'",
                    "EMPTY=",
                    "WITH_COMMENT=value # trailing comment",
                    "not a pair",
                ]
            )
        )
        self.assertEqual(parsed["JWT_SECRET"], "abc123")
        self.assertEqual(parsed["LLM_BASE_URL"], "https://example.test/v1")
        self.assertEqual(parsed["LLM_API_KEY"], "sk-test")
        self.assertEqual(parsed["EMPTY"], "")
        self.assertEqual(parsed["WITH_COMMENT"], "value")
        self.assertNotIn("not a pair", parsed)

    def test_llm_evidence_passes_when_env_is_complete(self):
        ok, detail = preflight.llm_evidence(
            {},
            "",
            environ={"LLM_BASE_URL": "https://example.test/v1", "LLM_API_KEY": "sk-secret"},
        )
        self.assertTrue(ok)
        self.assertIn("LLM_BASE_URL=set", detail)
        self.assertNotIn("sk-secret", detail)

    def test_llm_evidence_passes_from_backend_metrics(self):
        metrics = (
            'activity_descriptor_agent_calls_total{agent="interviewer",model="m",status="ok",code="ok"} 1\n'
        )
        ok, detail = preflight.llm_evidence({}, metrics, environ={})
        self.assertTrue(ok)
        self.assertIn("interviewer model calls", detail)

    def test_llm_evidence_warns_without_provider(self):
        ok, detail = preflight.llm_evidence({}, "activity_descriptor_runs_total 0\n", environ={})
        self.assertFalse(ok)
        self.assertIn("LLM_BASE_URL", detail)


class TimeAndFormatTests(unittest.TestCase):
    def test_parse_iso_datetime_supports_zulu_and_offsets(self):
        zulu = preflight.parse_iso_datetime("2026-11-18T18:37:46Z")
        offset = preflight.parse_iso_datetime("2026-11-18T21:37:46+03:00")
        self.assertIsNotNone(zulu)
        self.assertIsNotNone(offset)
        self.assertEqual(zulu, offset)
        self.assertEqual(zulu.tzinfo, timezone.utc)

    def test_parse_iso_datetime_rejects_garbage(self):
        self.assertIsNone(preflight.parse_iso_datetime("not-a-date"))
        self.assertIsNone(preflight.parse_iso_datetime(None))

    def test_format_duration(self):
        self.assertEqual(preflight.format_duration(0), "0s")
        self.assertEqual(preflight.format_duration(45), "45s")
        self.assertEqual(preflight.format_duration(90), "1m")
        self.assertEqual(preflight.format_duration(3700), "1h 1m")
        self.assertEqual(preflight.format_duration(90000), "1d 1h")

    def test_format_age(self):
        started = (datetime.now(timezone.utc) - timedelta(hours=2)).isoformat()
        self.assertEqual(preflight.format_age(started), "2h 0m")
        self.assertEqual(preflight.format_age("not-a-date"), "?")

    def test_join_url(self):
        self.assertEqual(preflight.join_url("http://x:1", "/api/v1/x"), "http://x:1/api/v1/x")
        self.assertEqual(preflight.join_url("http://x:1/", "api"), "http://x:1/api")

    def test_profession_stepik_catalogue(self):
        self.assertTrue(preflight.profession_has_stepik_courses("Data Analyst"))
        self.assertTrue(preflight.profession_has_stepik_courses("Аналитик данных"))
        self.assertTrue(preflight.profession_has_stepik_courses("Backend Developer"))
        self.assertFalse(preflight.profession_has_stepik_courses("Повар"))

    def test_error_code_extracts_stable_code(self):
        response = preflight.HttpResponse(500, "http://x", b'{"code":"INTERNAL","message":"boom"}')
        self.assertEqual(preflight.error_code(response), "INTERNAL")
        plain = preflight.HttpResponse(500, "http://x", b"plain body")
        self.assertEqual(preflight.error_code(plain), "plain body")


class DataSelectionTests(unittest.TestCase):
    def test_cleanup_candidates_are_failed_runs_only(self):
        rows = [
            _row("done-1", "done"),
            _row("failed-1", "failed"),
            _row("running-1", "running"),
            _row("degraded-1", "degraded"),
            _row("failed-2", "failed"),
        ]
        candidates = data.failed_cleanup_candidates(rows)
        self.assertEqual([row.id for row in candidates], ["failed-1", "failed-2"])
        for row in candidates:
            self.assertEqual(row.status, "failed")

    def test_cleanup_candidates_honour_older_than(self):
        rows = [
            _row("recent-failed", "failed", started_days_ago=1),
            _row("old-failed", "failed", started_days_ago=30),
        ]
        candidates = data.failed_cleanup_candidates(rows, older_than_days=7)
        self.assertEqual([row.id for row in candidates], ["old-failed"])

    def test_keep_completed_returns_only_newest_completed(self):
        rows = [
            _row("done-new", "done", started_days_ago=1),
            _row("failed", "failed", started_days_ago=2),
            _row("degraded", "degraded", started_days_ago=3),
            _row("done-old", "done", started_days_ago=4),
        ]
        kept = data.keep_completed(rows, 2)
        self.assertEqual([row.id for row in kept], ["done-new", "degraded"])
        self.assertEqual(data.keep_completed(rows, 0), [])

    def test_filtered_rows_by_status_and_age(self):
        rows = [
            _row("recent-failed", "failed", started_days_ago=1),
            _row("old-failed", "failed", started_days_ago=30),
            _row("done", "done", started_days_ago=40),
        ]
        self.assertEqual([row.id for row in data.filtered_rows(rows, "failed", None)], ["recent-failed", "old-failed"])
        self.assertEqual([row.id for row in data.filtered_rows(rows, "failed", 7)], ["old-failed"])
        self.assertEqual([row.id for row in data.filtered_rows(rows, "all", 7)], ["old-failed", "done"])

    def test_count_by_status(self):
        rows = [_row("a", "done"), _row("b", "failed"), _row("c", "failed"), _row("d", "running")]
        counts = data.count_by_status(rows)
        self.assertEqual(counts["done"], 1)
        self.assertEqual(counts["failed"], 2)
        self.assertEqual(counts["running"], 1)
        self.assertEqual(counts["degraded"], 0)

    def test_render_table_contains_headers_and_run_ids(self):
        rendered = data.render_table([_row("run-1", "done", nodes=11)])
        self.assertIn("ID", rendered)
        self.assertIn("NODES", rendered)
        self.assertIn("run-1", rendered)
        self.assertIn("11", rendered)


class CleanupSafetyTests(unittest.TestCase):
    def setUp(self):
        self.out = data.Output(json_mode=True)

    def test_cleanup_dry_run_deletes_nothing(self):
        client = FakeClient()
        rows = [_row("failed-1", "failed"), _row("done-1", "done")]
        result = data.cleanup_failed(client, rows, None, yes=False, out=self.out)
        self.assertEqual(client.deleted, [])
        self.assertTrue(result["dry_run"])
        self.assertEqual(result["planned"], ["failed-1"])

    def test_cleanup_with_yes_deletes_only_failed_runs(self):
        client = FakeClient()
        rows = [_row("failed-1", "failed"), _row("done-1", "done"), _row("running-1", "running")]
        result = data.cleanup_failed(client, rows, None, yes=True, out=self.out)
        self.assertEqual(client.deleted, ["failed-1"])
        self.assertEqual(result["deleted"], ["failed-1"])

    def test_cleanup_never_touches_done_even_when_older_than_zero(self):
        client = FakeClient()
        rows = [_row("done-old", "done", started_days_ago=100), _row("failed-old", "failed", started_days_ago=100)]
        data.cleanup_failed(client, rows, 0, yes=True, out=self.out)
        self.assertEqual(client.deleted, ["failed-old"])

    def test_cleanup_handles_not_found_as_already_gone(self):
        client = FakeClient(status=404)
        rows = [_row("failed-1", "failed")]
        result = data.cleanup_failed(client, rows, None, yes=True, out=self.out)
        self.assertEqual(result["already_gone"], ["failed-1"])
        self.assertEqual(result["errors"], [])

    def test_explicit_delete_refuses_completed_run(self):
        client = FakeClient()
        rows = [_row("done-1", "done")]
        result = data.delete_explicit(client, rows, ["done-1"], yes=True, allow_completed=False, out=self.out)
        self.assertEqual(client.deleted, [])
        self.assertEqual(len(result["refused"]), 1)
        self.assertEqual(result["refused"][0]["id"], "done-1")

    def test_explicit_delete_completed_needs_allow_completed(self):
        client = FakeClient()
        rows = [_row("done-1", "done")]
        result = data.delete_explicit(client, rows, ["done-1"], yes=True, allow_completed=True, out=self.out)
        self.assertEqual(client.deleted, ["done-1"])
        self.assertEqual(result["deleted"], ["done-1"])

    def test_explicit_delete_dry_run_deletes_nothing(self):
        client = FakeClient()
        rows = [_row("failed-1", "failed")]
        result = data.delete_explicit(client, rows, ["failed-1"], yes=False, allow_completed=False, out=self.out)
        self.assertEqual(client.deleted, [])
        self.assertTrue(result["dry_run"])

    def test_explicit_unknown_id_is_attempted_once(self):
        client = FakeClient(status=404)
        result = data.delete_explicit(client, [], ["not-in-list"], yes=True, allow_completed=False, out=self.out)
        self.assertEqual(client.deleted, ["not-in-list"])
        self.assertEqual(result["already_gone"], ["not-in-list"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
