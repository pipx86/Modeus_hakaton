#!/usr/bin/env python3
"""Validate the committed n8n workflow exports in this directory.

Standard library only, so it can run anywhere (no Docker/n8n needed):

    python3 deploy/n8n/workflows/validate_workflows.py
    python3 deploy/n8n/workflows/validate_workflows.py notion_email_notifications.json

Checks for every ``*.json`` file:

* valid JSON with the keys of an n8n workflow export (``name``, ``nodes``,
  ``connections``, ``settings``, ``active``);
* every node has ``id``, ``name``, ``type``, ``typeVersion``, ``position`` and
  ``parameters``; node names and IDs are unique; positions are ``[x, y]``;
* every connection source and target refers to an existing node; IF nodes
  expose two outputs, nodes with ``onError: continueErrorOutput`` expose two
  branches, trigger nodes have no incoming connections;
* ``active`` is ``false`` (exports are imported inactive) and execution
  history settings are present;
* every node is reachable from a trigger;
* reports the ``$env.*`` variables, disabled nodes and retry settings used by
  the workflow (useful when checking deployment wiring by hand).

Exit code 0 when all files are valid, 1 otherwise.
"""

from __future__ import annotations

import json
import os
import re
import sys

WORKFLOW_DIR = os.path.dirname(os.path.abspath(__file__))
TRIGGER_TYPES = {
    "n8n-nodes-base.manualTrigger",
    "n8n-nodes-base.scheduleTrigger",
    "n8n-nodes-base.webhook",
}
REQUIRED_TOP_LEVEL = ("name", "nodes", "connections", "settings", "active")
REQUIRED_NODE_KEYS = ("id", "name", "type", "typeVersion", "position", "parameters")
REQUIRED_SETTINGS = (
    "saveExecutionProgress",
    "saveDataErrorExecution",
    "saveDataSuccessExecution",
)


class ValidationError(Exception):
    """Raised when a workflow export fails a structural check."""


def load(path: str) -> dict:
    try:
        with open(path, encoding="utf-8") as handle:
            return json.load(handle)
    except OSError as exc:
        raise ValidationError(f"cannot read file: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise ValidationError(f"invalid JSON: {exc}") from exc


def check_workflow(path: str, workflow: dict) -> dict:
    rel = os.path.basename(path)
    for key in REQUIRED_TOP_LEVEL:
        if key not in workflow:
            raise ValidationError(f"{rel}: missing top-level key {key!r}")
    if workflow["active"] is not False:
        raise ValidationError(f"{rel}: must be imported inactive ('active': false)")
    for key in REQUIRED_SETTINGS:
        if key not in workflow["settings"]:
            raise ValidationError(f"{rel}: settings are missing {key!r}")

    nodes = workflow["nodes"]
    if not isinstance(nodes, list) or len(nodes) < 3:
        raise ValidationError(f"{rel}: expected a list of at least 3 nodes")

    names: set[str] = set()
    ids: set[str] = set()
    node_types: dict[str, str] = {}
    for node in nodes:
        for key in REQUIRED_NODE_KEYS:
            if key not in node:
                raise ValidationError(f"{rel}: node {node.get('name')!r} is missing {key!r}")
        name = node["name"]
        if name in names or node["id"] in ids:
            raise ValidationError(f"{rel}: duplicate node name/id {name!r}")
        names.add(name)
        ids.add(node["id"])
        node_types[name] = node["type"]
        position = node["position"]
        if not (isinstance(position, list) and len(position) == 2):
            raise ValidationError(f"{rel}: node {name!r} has an invalid position")

    triggers = {name for name, node_type in node_types.items() if node_type in TRIGGER_TYPES}
    if not triggers:
        raise ValidationError(f"{rel}: no trigger node (manual/schedule/webhook) found")

    connections = workflow["connections"]
    targets: set[str] = set()
    for source, output in connections.items():
        if source not in names:
            raise ValidationError(f"{rel}: connection source {source!r} is not a node")
        branches = output.get("main")
        if not isinstance(branches, list):
            raise ValidationError(f"{rel}: connection {source!r} has no 'main' branches")
        for branch in branches:
            for connection in branch:
                target = connection.get("node")
                if target not in names:
                    raise ValidationError(f"{rel}: connection target {target!r} is not a node")
                targets.add(target)

    for name in sorted(triggers):
        if name in targets:
            raise ValidationError(f"{rel}: trigger node {name!r} has incoming connections")
    for trigger in triggers:
        if trigger not in connections:
            raise ValidationError(f"{rel}: trigger node {trigger!r} has no outgoing connections")

    for node in nodes:
        name = node["name"]
        branches = connections.get(name, {}).get("main", [])
        if node["type"] == "n8n-nodes-base.if" and len(branches) != 2:
            raise ValidationError(f"{rel}: IF node {name!r} must have two output branches")
        if node.get("onError") == "continueErrorOutput" and len(branches) != 2:
            raise ValidationError(
                f"{rel}: node {name!r} has onError=continueErrorOutput but not two branches"
            )

    # Reachability: every node must be reachable from a trigger.
    edges: dict[str, set[str]] = {name: set() for name in names}
    for source, output in connections.items():
        for branch in output.get("main", []):
            for connection in branch:
                edges[source].add(connection["node"])
    reached: set[str] = set(triggers)
    queue = list(triggers)
    while queue:
        current = queue.pop()
        for target in edges.get(current, set()):
            if target not in reached:
                reached.add(target)
                queue.append(target)
    unreachable = sorted(names - reached)
    if unreachable:
        raise ValidationError(f"{rel}: nodes not reachable from a trigger: {unreachable}")

    disabled = sorted(node["name"] for node in nodes if node.get("disabled"))
    retrying = sorted(
        f"{node['name']} (maxTries={node.get('maxTries')}, onError={node.get('onError')})"
        for node in nodes
        if node.get("retryOnFail")
    )
    env_vars = sorted(set(re.findall(r"\$env\.([A-Z0-9_]+)", json.dumps(workflow))))
    placeholders = sorted(
        f"{provider}:{credential.get('id')}"
        for node in nodes
        for provider, credential in (node.get("credentials") or {}).items()
        if str(credential.get("id", "")).startswith("REPLACE_WITH")
    )

    return {
        "file": rel,
        "name": workflow["name"],
        "nodes": len(nodes),
        "triggers": sorted(triggers),
        "disabled": disabled,
        "retrying": retrying,
        "env": env_vars,
        "placeholder_credentials": placeholders,
    }


def main(argv: list[str]) -> int:
    if len(argv) > 1 and argv[1] in {"-h", "--help"}:
        print(__doc__)
        return 0

    if len(argv) > 1:
        paths = [os.path.join(WORKFLOW_DIR, name) for name in argv[1:]]
    else:
        paths = sorted(
            os.path.join(WORKFLOW_DIR, name)
            for name in os.listdir(WORKFLOW_DIR)
            if name.endswith(".json")
        )
    if not paths:
        print("FAIL: no workflow exports found", file=sys.stderr)
        return 1

    failed = False
    for path in paths:
        print(f"\n== {os.path.basename(path)} ==")
        try:
            summary = check_workflow(path, load(path))
        except ValidationError as exc:
            print(f"FAIL: {exc}", file=sys.stderr)
            failed = True
            continue
        print(f"  name: {summary['name']}")
        print(f"  nodes: {summary['nodes']} (triggers: {', '.join(summary['triggers'])})")
        print(f"  env vars: {summary['env'] or '-'}")
        print(f"  disabled nodes: {summary['disabled'] or '-'}")
        print(f"  retrying nodes: {summary['retrying'] or '-'}")
        print(f"  placeholder credentials: {summary['placeholder_credentials'] or '-'}")
        print("  OK")

    if failed:
        print("\nFAIL: at least one workflow export is invalid", file=sys.stderr)
        return 1
    print(f"\nOK: validated {len(paths)} workflow export(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
