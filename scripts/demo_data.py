#!/usr/bin/env python3
"""Safe inspection and cleanup helper for the demo database.

The default action is a read-only report: it lists every run with status,
profession, start time, age and tree size, and highlights failed runs as
cleanup candidates. Nothing is ever deleted without an explicit action flag AND
an explicit --yes:

* --cleanup-failed deletes only runs whose status is exactly "failed";
* --delete-run <id> [<id> ...] deletes only the runs explicitly named by id and
  refuses completed (done/degraded) runs unless --allow-completed is given;
* --keep-completed N is report-only and never deletes anything;
* without --yes every action is a dry run that prints the plan.

The script is stdlib-only, cross-platform and never prints secrets.

Examples:
    python scripts/demo_data.py                          # report, no changes
    python scripts/demo_data.py --status failed
    python scripts/demo_data.py --older-than 7 --keep-completed 3
    python scripts/demo_data.py --cleanup-failed         # dry run
    python scripts/demo_data.py --cleanup-failed --yes   # deletes failed runs only
    python scripts/demo_data.py --delete-run <run-id> --yes
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

# Keep the repository clean: importing demo_preflight must not leave
# scripts/__pycache__ behind on demo machines.
sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))

from demo_preflight import (  # noqa: E402  (import after sys.path tweak)
    ApiClient,
    DemoAPIError,
    clip,
    error_code,
    format_age,
    load_env_files,
    parse_iso_datetime,
    resolve_token,
)

EXIT_OK = 0
EXIT_FAIL = 1

COMPLETED_STATUSES = {"done", "degraded"}
FAILED_STATUS = "failed"
KNOWN_STATUSES = ("pending", "running", "done", "degraded", "failed")


@dataclass
class RunRow:
    """One run as reported by GET /api/v1/runs plus the optional tree size."""

    id: str
    status: str
    profession: str
    stage: str
    started_at: str
    finished_at: str
    nodes: int | None = None

    @property
    def age_days(self) -> float | None:
        started = parse_iso_datetime(self.started_at)
        if started is None:
            return None
        return (datetime.now(timezone.utc) - started).total_seconds() / 86400.0


class Output:
    """Print helper that stays quiet in --json mode."""

    def __init__(self, json_mode: bool):
        self.json_mode = json_mode

    def line(self, text: str = "") -> None:
        if not self.json_mode:
            print(text, flush=True)


def run_rows_from_payload(runs: list, nodes_by_id: dict[str, int | None]) -> list[RunRow]:
    rows: list[RunRow] = []
    for run in runs:
        if not isinstance(run, dict):
            continue
        run_id = str(run.get("id") or "")
        rows.append(
            RunRow(
                id=run_id,
                status=str(run.get("status") or "?"),
                profession=str(run.get("profession") or ""),
                stage=str(run.get("stage") or ""),
                started_at=str(run.get("started_at") or ""),
                finished_at=str(run.get("finished_at") or ""),
                nodes=nodes_by_id.get(run_id),
            )
        )
    return rows


def failed_cleanup_candidates(rows: list[RunRow], older_than_days: float | None = None) -> list[RunRow]:
    """Return failed runs only; this is the single source of truth for cleanup.

    The status check is strict on purpose: done/degraded/running runs must never
    become deletion candidates, even if the caller passes an age filter.
    """

    candidates: list[RunRow] = []
    for row in rows:
        if row.status != FAILED_STATUS:
            continue
        if older_than_days is not None:
            age = row.age_days
            if age is None or age < older_than_days:
                continue
        candidates.append(row)
    return candidates


def keep_completed(rows: list[RunRow], keep: int) -> list[RunRow]:
    """Return the newest `keep` completed runs (report-only protection)."""

    if keep <= 0:
        return []
    completed = [row for row in rows if row.status in COMPLETED_STATUSES]
    return completed[:keep]


def filtered_rows(rows: list[RunRow], status_filter: str, older_than_days: float | None) -> list[RunRow]:
    selected = rows
    if status_filter != "all":
        selected = [row for row in selected if row.status == status_filter]
    if older_than_days is not None:
        selected = [
            row
            for row in selected
            if row.age_days is not None and row.age_days >= older_than_days
        ]
    return selected


def count_by_status(rows: list[RunRow]) -> dict[str, int]:
    counts = {status: 0 for status in KNOWN_STATUSES}
    counts["other"] = 0
    for row in rows:
        counts[row.status if row.status in counts else "other"] += 1
    return counts


def _clip(text: str, limit: int) -> str:
    return clip(text, limit)


def render_table(rows: list[RunRow]) -> str:
    headers = ("ID", "STATUS", "PROFESSION", "STAGE", "STARTED", "AGE", "NODES")
    body: list[tuple[str, ...]] = []
    for row in rows:
        body.append(
            (
                row.id,
                row.status,
                _clip(row.profession, 28),
                _clip(row.stage, 10),
                row.started_at.replace("T", " ")[:19] if row.started_at else "?",
                format_age(row.started_at),
                "?" if row.nodes is None else str(row.nodes),
            )
        )
    widths = [len(header) for header in headers]
    for cells in body:
        for index, cell in enumerate(cells):
            widths[index] = max(widths[index], len(cell))
    header_line = "  ".join(header.ljust(widths[index]) for index, header in enumerate(headers))
    separator = "  ".join("-" * width for width in widths)
    lines = [header_line, separator]
    for cells in body:
        lines.append("  ".join(cell.ljust(widths[index]) for index, cell in enumerate(cells)))
    return "\n".join(lines)


def render_run_list(rows: list[RunRow], title: str) -> list[str]:
    lines = [title, ""]
    if not rows:
        lines.append("  (none)")
        return lines
    lines.append(render_table(rows))
    return lines


def fetch_rows(client: ApiClient, include_nodes: bool) -> tuple[list[RunRow], dict[str, int | None]]:
    """Fetch runs and (optionally) the tree size of completed runs."""

    payload = client.get_json("/api/v1/runs")
    if not isinstance(payload, list):
        raise DemoAPIError("GET /api/v1/runs returned an unexpected payload (expected a JSON array)")
    nodes_by_id: dict[str, int | None] = {}
    if include_nodes:
        for run in payload:
            if not isinstance(run, dict):
                continue
            if run.get("status") not in COMPLETED_STATUSES:
                continue
            run_id = str(run.get("id") or "")
            if not run_id:
                continue
            try:
                response = client.get(f"/api/v1/runs/{run_id}/tree")
            except DemoAPIError:
                nodes_by_id[run_id] = None
                continue
            if response.status == 200:
                try:
                    tree = response.json()
                except DemoAPIError:
                    nodes_by_id[run_id] = None
                else:
                    nodes_by_id[run_id] = len(tree) if isinstance(tree, list) else None
            else:
                nodes_by_id[run_id] = None
    return run_rows_from_payload(payload, nodes_by_id), nodes_by_id


def print_report(out: Output, rows: list[RunRow], args) -> None:
    counts = count_by_status(rows)
    out.line(f"Demo data report: {args.base_url}")
    out.line(
        "Runs: {total} total | done={done} failed={failed} running={running} "
        "degraded={degraded} pending={pending}".format(
            total=len(rows),
            done=counts.get("done", 0),
            failed=counts.get("failed", 0),
            running=counts.get("running", 0),
            degraded=counts.get("degraded", 0),
            pending=counts.get("pending", 0),
        )
    )
    if rows:
        newest = max(rows, key=lambda row: row.started_at or "")
        oldest = min(rows, key=lambda row: row.started_at or "")
        out.line(f"Newest run: {newest.id} ({newest.started_at or '?'})")
        out.line(f"Oldest run: {oldest.id} ({oldest.started_at or '?'})")
    out.line("")

    view = filtered_rows(rows, args.status, args.older_than)
    title = "RUNS"
    if args.status != "all" or args.older_than is not None:
        title += f" (filter: status={args.status}" + (
            f", older-than={args.older_than:g}d)" if args.older_than is not None else ")"
        )
    for line in render_run_list(view, title):
        out.line(line)
    out.line("")

    failed = [row for row in view if row.status == FAILED_STATUS]
    for line in render_run_list(failed, f"FAILED RUNS ({len(failed)}) - cleanup candidates"):
        out.line(line)
    if failed:
        out.line("Review the plan with --cleanup-failed; add --yes to delete only status=failed runs.")
    out.line("")

    running = [row for row in view if row.status == "running"]
    if running:
        for line in render_run_list(
            running,
            f"RUNNING RUNS ({len(running)}) - not eligible for --cleanup-failed",
        ):
            out.line(line)
        out.line(
            "A run that has been 'running' for hours is usually stale. Delete it only after "
            "confirming the backend is not processing it: --delete-run <id> --yes"
        )
        out.line("")

    kept = keep_completed(rows, args.keep_completed)
    if args.keep_completed > 0:
        for line in render_run_list(
            kept,
            f"KEEP NEWEST {args.keep_completed} COMPLETED RUN(S) - report only, never deleted by --cleanup-failed",
        ):
            out.line(line)
        out.line("")


def cleanup_failed(client: ApiClient, rows: list[RunRow], older_than_days: float | None, yes: bool, out: Output) -> dict:
    """Delete only failed runs. Returns a JSON-friendly result summary."""

    result: dict = {
        "action": "cleanup_failed",
        "planned": [],
        "deleted": [],
        "already_gone": [],
        "refused": [],
        "errors": [],
        "dry_run": not yes,
    }
    candidates = failed_cleanup_candidates(rows, older_than_days)
    if not candidates:
        out.line("Cleanup: no runs with status=failed match the current filters; nothing to do.")
        return result

    result["planned"] = [row.id for row in candidates]
    out.line(f"Cleanup plan: delete {len(candidates)} run(s) with status=failed (nothing else is eligible):")
    for row in candidates:
        out.line(f"  {row.id}  status={row.status}  profession={_clip(row.profession, 40)}  started={row.started_at}")
    if not yes:
        out.line("")
        out.line("DRY RUN: no data was deleted.")
        out.line("Re-run with --cleanup-failed --yes to delete these failed runs.")
        return result

    out.line("")
    for row in candidates:
        try:
            response = client.delete(f"/api/v1/runs/{row.id}")
        except DemoAPIError as exc:
            result["errors"].append({"id": row.id, "error": str(exc)})
            out.line(f"  ERROR {row.id}: {exc}")
            continue
        if response.status == 204:
            result["deleted"].append(row.id)
            out.line(f"  deleted {row.id} (status=failed)")
        elif response.status == 404:
            result["already_gone"].append(row.id)
            out.line(f"  skipped {row.id}: already gone (HTTP 404)")
        elif response.status in (401, 403):
            result["errors"].append({"id": row.id, "error": f"HTTP {response.status} {error_code(response)}".strip()})
            out.line(
                f"  ERROR: HTTP {response.status} {error_code(response)} - an admin token is required; "
                "issue one with: go run ./cmd/local-token -role admin"
            )
            break
        else:
            result["errors"].append({"id": row.id, "error": f"HTTP {response.status} {error_code(response)}".strip()})
            out.line(f"  ERROR {row.id}: HTTP {response.status} {error_code(response)}")
    out.line("")
    out.line(
        "RESULT: deleted {deleted}, already gone {gone}, errors {errors} (failed runs only).".format(
            deleted=len(result["deleted"]),
            gone=len(result["already_gone"]),
            errors=len(result["errors"]),
        )
    )
    return result


def delete_explicit(
    client: ApiClient,
    rows: list[RunRow],
    ids: list[str],
    yes: bool,
    allow_completed: bool,
    out: Output,
) -> dict:
    """Delete only the runs explicitly named by id, protecting completed runs."""

    result: dict = {
        "action": "delete_run",
        "planned": [],
        "deleted": [],
        "already_gone": [],
        "refused": [],
        "errors": [],
        "dry_run": not yes,
    }
    by_id = {row.id.lower(): row for row in rows}
    plan: list[RunRow | str] = []
    for raw_id in ids:
        run_id = raw_id.strip()
        if not run_id:
            continue
        row = by_id.get(run_id.lower())
        if row is None:
            plan.append(run_id)
            continue
        if row.status in COMPLETED_STATUSES and not allow_completed:
            result["refused"].append({"id": row.id, "reason": f"status={row.status} is a completed run"})
            out.line(
                f"REFUSED {row.id}: status={row.status} is a completed run. "
                "Completed runs are never deleted silently; add --allow-completed to override."
            )
            continue
        plan.append(row)

    executable = [item for item in plan if isinstance(item, RunRow)]
    unknown = [item for item in plan if isinstance(item, str)]
    result["planned"] = [item.id if isinstance(item, RunRow) else item for item in plan]
    result["unknown_ids"] = unknown
    if not plan:
        out.line("Explicit delete: nothing to do.")
        return result

    out.line(f"Explicit delete plan: {len(executable)} known run(s), {len(unknown)} unknown id(s):")
    for item in executable:
        out.line(f"  {item.id}  status={item.status}  profession={_clip(item.profession, 40)}")
    for run_id in unknown:
        out.line(f"  {run_id}  (not present in GET /api/v1/runs; DELETE will be attempted)")
    if not yes:
        out.line("")
        out.line("DRY RUN: no data was deleted.")
        out.line("Re-run with --delete-run <id> --yes to delete exactly these runs.")
        return result

    out.line("")
    for item in executable:
        try:
            response = client.delete(f"/api/v1/runs/{item.id}")
        except DemoAPIError as exc:
            result["errors"].append({"id": item.id, "error": str(exc)})
            out.line(f"  ERROR {item.id}: {exc}")
            continue
        if response.status == 204:
            result["deleted"].append(item.id)
            out.line(f"  deleted {item.id} (status={item.status})")
        elif response.status == 404:
            result["already_gone"].append(item.id)
            out.line(f"  skipped {item.id}: already gone (HTTP 404)")
        elif response.status in (401, 403):
            result["errors"].append({"id": item.id, "error": f"HTTP {response.status} {error_code(response)}".strip()})
            out.line(
                f"  ERROR: HTTP {response.status} {error_code(response)} - an admin token is required; "
                "issue one with: go run ./cmd/local-token -role admin"
            )
            break
        else:
            result["errors"].append({"id": item.id, "error": f"HTTP {response.status} {error_code(response)}".strip()})
            out.line(f"  ERROR {item.id}: HTTP {response.status} {error_code(response)}")
    for run_id in unknown:
        try:
            response = client.delete(f"/api/v1/runs/{run_id}")
        except DemoAPIError as exc:
            result["errors"].append({"id": run_id, "error": str(exc)})
            out.line(f"  ERROR {run_id}: {exc}")
            continue
        if response.status == 204:
            result["deleted"].append(run_id)
            out.line(f"  deleted {run_id}")
        elif response.status == 404:
            result["already_gone"].append(run_id)
            out.line(f"  skipped {run_id}: not found (HTTP 404)")
        else:
            result["errors"].append({"id": run_id, "error": f"HTTP {response.status} {error_code(response)}".strip()})
            out.line(f"  ERROR {run_id}: HTTP {response.status} {error_code(response)}")
    out.line("")
    out.line(
        "RESULT: deleted {deleted}, already gone {gone}, refused {refused}, errors {errors}.".format(
            deleted=len(result["deleted"]),
            gone=len(result["already_gone"]),
            refused=len(result["refused"]),
            errors=len(result["errors"]),
        )
    )
    return result


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Inspect and safely clean up demo runs. Default action is a read-only report.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "examples:\n"
            "  python scripts/demo_data.py\n"
            "  python scripts/demo_data.py --status failed\n"
            "  python scripts/demo_data.py --older-than 7 --keep-completed 3\n"
            "  python scripts/demo_data.py --cleanup-failed          # dry run\n"
            "  python scripts/demo_data.py --cleanup-failed --yes    # delete failed runs only\n"
            "  python scripts/demo_data.py --delete-run <run-id> --yes\n"
        ),
    )
    parser.add_argument("--base-url", default=os.environ.get("DEMO_API_URL", "http://127.0.0.1:8080"), help="backend base URL (default http://127.0.0.1:8080)")
    parser.add_argument("--token", help="already issued admin JWT (never printed); default: DEMO_TOKEN/API_TOKEN or mint from JWT_SECRET")
    parser.add_argument("--http-timeout", type=float, default=20.0, help="single HTTP request timeout in seconds (default 20)")
    parser.add_argument("--status", choices=("all",) + KNOWN_STATUSES, default="all", help="filter the report table by run status (default all)")
    parser.add_argument("--older-than", type=float, metavar="DAYS", help="only show/cleanup runs started at least DAYS days ago")
    parser.add_argument("--keep-completed", type=int, default=0, metavar="N", help="report the N newest completed runs; never deletes anything")
    parser.add_argument("--no-nodes", action="store_true", help="do not fetch tree sizes for completed runs (faster report)")
    parser.add_argument("--json", action="store_true", help="print one machine-readable JSON report to stdout")
    parser.add_argument("--yes", action="store_true", help="confirm the destructive action; without it every action is a dry run")

    actions = parser.add_mutually_exclusive_group()
    actions.add_argument(
        "--cleanup-failed",
        action="store_true",
        help="delete runs with status=failed only; requires --yes to actually delete",
    )
    actions.add_argument(
        "--delete-run",
        nargs="+",
        metavar="ID",
        help="delete only the explicitly listed run ids; completed runs require --allow-completed",
    )
    parser.add_argument(
        "--allow-completed",
        action="store_true",
        help="allow --delete-run to delete completed (done/degraded) runs; never affects --cleanup-failed",
    )
    return parser


def _validate_args(parser: argparse.ArgumentParser, args) -> None:
    if args.http_timeout <= 0:
        parser.error("--http-timeout must be positive")
    if args.older_than is not None and args.older_than < 0:
        parser.error("--older-than must be zero or positive")
    if args.keep_completed < 0:
        parser.error("--keep-completed must be zero or positive")
    if args.yes and not (args.cleanup_failed or args.delete_run):
        parser.error("--yes has no effect without --cleanup-failed or --delete-run; the default action is a read-only report")


def main(argv=None) -> int:
    for stream in (sys.stdout, sys.stderr):
        try:
            stream.reconfigure(errors="replace")
        except (AttributeError, OSError):
            pass
    parser = build_parser()
    args = parser.parse_args(argv)
    _validate_args(parser, args)
    out = Output(args.json)

    dotenv = load_env_files()
    try:
        token, token_source = resolve_token(args.token, dotenv)
    except DemoAPIError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return EXIT_FAIL
    client = ApiClient(args.base_url, token, args.http_timeout)

    try:
        rows, _ = fetch_rows(client, include_nodes=not args.no_nodes)
    except DemoAPIError as exc:
        print(f"ERROR: cannot load runs: {exc}", file=sys.stderr)
        return EXIT_FAIL

    if args.json:
        # In JSON mode nothing is printed until the end, and nothing is deleted
        # unless the caller passed --yes explicitly.
        report: dict = {
            "base_url": args.base_url,
            "generated_at": datetime.now(timezone.utc).isoformat(),
            "token_source": token_source,
            "total": len(rows),
            "counts": count_by_status(rows),
            "filters": {"status": args.status, "older_than_days": args.older_than},
            "runs": [
                {
                    "id": row.id,
                    "status": row.status,
                    "profession": row.profession,
                    "stage": row.stage,
                    "started_at": row.started_at,
                    "finished_at": row.finished_at,
                    "age_days": round(row.age_days, 3) if row.age_days is not None else None,
                    "nodes": row.nodes,
                }
                for row in filtered_rows(rows, args.status, args.older_than)
            ],
            "failed_cleanup_candidates": [row.id for row in failed_cleanup_candidates(rows, args.older_than)],
            "keep_completed": [row.id for row in keep_completed(rows, args.keep_completed)],
            "cleanup": None,
            "delete_run": None,
        }
        exit_code = EXIT_OK
        if args.cleanup_failed:
            report["cleanup"] = cleanup_failed(client, rows, args.older_than, args.yes, out)
            if report["cleanup"]["errors"]:
                exit_code = EXIT_FAIL
        if args.delete_run:
            report["delete_run"] = delete_explicit(client, rows, args.delete_run, args.yes, args.allow_completed, out)
            if report["delete_run"]["errors"] or report["delete_run"]["refused"]:
                exit_code = EXIT_FAIL
        print(json.dumps(report, ensure_ascii=True, indent=2))
        return exit_code

    print_report(out, rows, args)

    exit_code = EXIT_OK
    if args.cleanup_failed:
        print()
        result = cleanup_failed(client, rows, args.older_than, args.yes, out)
        if result["errors"]:
            exit_code = EXIT_FAIL
    if args.delete_run:
        print()
        result = delete_explicit(client, rows, args.delete_run, args.yes, args.allow_completed, out)
        if result["errors"] or result["refused"]:
            exit_code = EXIT_FAIL
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
