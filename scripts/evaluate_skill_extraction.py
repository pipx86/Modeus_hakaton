"""Check saved control responses. Input files contain local test results, not API keys.

Usage: python scripts/evaluate_skill_extraction.py PATH_TO_CONTROL_RESULT.json [...]
These checks cover explicit acceptance cases; they do not replace semantic review.
"""
import json
from pathlib import Path
import sys


def evaluate(path):
    record = json.loads(Path(path).read_text(encoding="utf-8"))
    if record["status"] != 200:
        return {"file": Path(path).name, "passed": False, "failures": ["HTTP extraction failed"]}
    model = record["result"]
    definitions = {(d["id"], d["version"]): d for d in model["definitions"]}
    links = model["links"]
    grouped = {case: [definitions[(link["definition_id"], link["version"])]
                      for link in links if link["node_id"] == node]
               for case, node in record["nodes"].items()}
    checks = {}
    for case in ["mention", "rejected", "injection"]:
        checks[case + "_abstention"] = not grouped[case]
    for case, kind in [("knowledge", "knowledge"), ("backend", "skill"),
                       ("architect", "skill"), ("proficiency", "proficiency"),
                       ("unknown", "skill"), ("vacancy", "skill"),
                       ("tool_classification", "skill")]:
        checks[case + "_classification"] = bool(grouped[case]) and all(d["kind"] == kind for d in grouped[case])
    uses = lambda case: {u["tool_id"] for d in grouped[case] for u in d["tools"]}
    checks["backend_python_and_postgresql"] = {"python", "postgresql"} <= uses("backend")
    checks["architect_no_python"] = "postgresql" in uses("architect") and "python" not in uses("architect")
    checks["proficiency_has_supporting_dimensions"] = bool(grouped["proficiency"]) and all(d["scope"] and d["autonomy"] and d["criterion"] for d in grouped["proficiency"])
    checks["unknown_conditions_stay_empty"] = all(not d["conditions"] for d in grouped["unknown"])
    checks["unknown_criterion_stays_empty"] = all(not d["criterion"] for d in grouped["unknown"])
    checks["no_invented_tool_versions"] = all(not u.get("version") for d in model["definitions"] for u in d["tools"])
    checks["no_automatic_approval"] = all(link["status"] == "proposed" for link in links)
    vacancy_links = [link for link in links if link["node_id"] == record["nodes"]["vacancy"]]
    checks["vacancy_is_requirement"] = bool(vacancy_links) and all(link["basis"] == "requirement" for link in vacancy_links)
    checks["only_concrete_tools_in_tool_control"] = uses("tool_classification") == {"git", "postman"}
    git_usages = [u["usage"].lower() for d in grouped["tool_classification"] for u in d["tools"] if u["tool_id"] == "git"]
    checks["git_not_mapped_to_pull_request"] = bool(git_usages) and all(
        "ветк" in usage and not any(term in usage for term in ("pull request", "ревью", "review", "запрос на слияние"))
        for usage in git_usages
    )
    checks["no_unmentioned_tools"] = all(u["tool_id"] in {"python", "postgresql", "sql", "docker", "git", "postman"} for d in model["definitions"] for u in d["tools"])
    warnings = []
    if "sql" not in uses("architect"):
        warnings.append("SQL omitted as an instrument in architect case despite SQL schema in source")
    return {"file": Path(path).name, "passed": all(checks.values()), "checks": checks,
            "failures": [key for key, passed in checks.items() if not passed], "warnings": warnings,
            "definitions": len(model["definitions"]), "links": len(links), "elapsed": record["elapsed"]}


if __name__ == "__main__":
    results = [evaluate(path) for path in sys.argv[1:]]
    print(json.dumps(results, ensure_ascii=False, indent=2))
    sys.exit(0 if results and all(result["passed"] for result in results) else 1)
