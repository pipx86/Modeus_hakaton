"""Local smoke check using disposable synthetic records; never prints credentials."""
from pathlib import Path
import base64
import copy
import hashlib
import hmac
import json
import subprocess
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def sql(statement):
    result = subprocess.run(
        ["docker", "compose", "exec", "-T", "postgres", "sh", "-c",
         'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At'],
        input=statement, text=True, encoding="utf-8", capture_output=True, cwd=ROOT,
    )
    if result.returncode:
        raise RuntimeError("Local PostgreSQL operation failed: " + result.stderr[-1500:])


def main():
    secret = ""
    for path in [ROOT / ".env", ROOT / ".secrets/llm.env"]:
        if path.exists():
            for line in path.read_text(encoding="utf-8-sig").splitlines():
                key, separator, value = line.partition("=")
                if separator and key.strip() == "JWT_SECRET":
                    secret = value.strip().strip("\"'")
    if not secret:
        raise RuntimeError("JWT_SECRET is absent in existing local configuration")
    encode = lambda value: base64.urlsafe_b64encode(value).decode().rstrip("=")
    header = encode(json.dumps({"alg": "HS256", "typ": "JWT"}).encode())
    claims = encode(json.dumps({"sub": "local-skill-smoke", "role": "admin", "exp": int(time.time()) + 300}).encode())
    unsigned = header + "." + claims
    token = unsigned + "." + encode(hmac.new(secret.encode(), unsigned.encode(), hashlib.sha256).digest())
    run, node, source, evidence = [str(uuid.uuid4()) for _ in range(4)]
    base = f"http://localhost:8083/api/v1/runs/{run}/skill-model"

    def request(method, suffix="", body=None, expected=200):
        payload = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(base + suffix, payload, headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"}, method=method)
        try:
            with urllib.request.urlopen(req, timeout=20) as response:
                status, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, raw = error.code, error.read()
        if status != expected:
            raise RuntimeError(f"{method} {suffix or '/'} returned {status}, expected {expected}")
        return json.loads(raw) if raw else None

    try:
        sql(f"""BEGIN;
INSERT INTO runs(id,profession,status) VALUES('{run}','__skill_model_smoke__','done');
INSERT INTO sources(id,run_id,type,title) VALUES('{source}','{run}','document','__skill_model_smoke__');
INSERT INTO nodes(id,run_id,type,title) VALUES('{node}','{run}','role','Synthetic transaction role');
UPDATE runs SET root_node_id='{node}' WHERE id='{run}';
INSERT INTO evidence(id,node_id,run_id,source_id,ref_id,quote,claim_origin) VALUES('{evidence}','{node}','{run}','{source}','smoke','Synthetic — original quote','source_statement');
COMMIT;""")
        model = request("GET")
        assert model["revision"] == 0
        assert len(request("GET", "/nodes")) == 1
        definition = dict(id="transaction", version=1, title="Atomic update", kind="skill", action="Implement", object="transaction", conditions="Failure between operations", result="No partial writes", criterion="Rollback scenario", autonomy="", scope="", tools=[dict(tool_id="postgresql", usage="Transaction", required=True)])
        model["definitions"].append(definition)
        model["links"].append(dict(id="binding", node_id=node, definition_id="transaction", version=1, evidence_ids=[evidence], evidence=[], basis="requirement", reason="Synthetic source describes the operation", status="approved"))
        saved = request("PUT", body=model)
        assert saved["revision"] == 1
        assert saved["links"][0]["evidence"][0]["quote"] == "Synthetic — original quote"
        request("PUT", body=model, expected=409)
        invalid = copy.deepcopy(saved)
        invalid["definitions"][0]["title"] = "Silent overwrite"
        request("PUT", body=invalid, expected=422)
        reference = dict(definition_id="transaction", version=1)
        assert request("POST", "/compare", dict(expected=reference, actual=reference))["status"] == "full"
        next_definition = copy.deepcopy(definition)
        next_definition.update(version=2, title="Updated wording")
        saved["definitions"].append(next_definition)
        saved = request("PUT", body=saved)
        assert saved["links"][0]["version"] == 1
        assert request("GET", "?revision=1")["definitions"][0]["title"] == "Atomic update"
        sql(f"UPDATE evidence SET quote='Changed source quote' WHERE id='{evidence}';")
        saved = request("PUT", body=saved)
        assert saved["links"][0]["status"] == "proposed"
        sql(f"UPDATE evidence SET status='rejected' WHERE id='{evidence}';")
        saved = request("PUT", body=saved)
        assert saved["links"][0]["stale"] is True
        print("PASS: persistence, exact quotes, comparison, version history, 409 conflict, immutable definitions, changed/rejected evidence")
    finally:
        sql(f"BEGIN; DELETE FROM runs WHERE id='{run}' AND profession='__skill_model_smoke__'; DELETE FROM sources WHERE id='{source}' AND title='__skill_model_smoke__'; COMMIT;")


if __name__ == "__main__":
    main()
