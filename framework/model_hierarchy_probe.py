#!/usr/bin/env python3
"""Bounded online prompt/tool probe; no database or production configuration.

Read a test API key from stdin. Retain exact requests/responses (without HTTP
headers) and usage in a private directory. legacy reproduces the previous small
fixture; catalog uses the same task with full field types and consistent errors.
The call limit belongs to this test, not to Learner's investigation policy.
"""
import argparse
import getpass
import hashlib
import json
import os
import pathlib
import sys
import time
import urllib.error
import urllib.request

FIELDS = {
    "academic_offerings": ["id", "connection_id", "source", "course_ref", "course_name"],
    "academic_items": ["id", "connection_id", "offering_id", "title", "kind", "status"],
}
ROWS = {
    "academic_offerings": [{"id": "offering-fixture", "connection_id": "conn-fixture", "source": "chaoxing", "course_ref": "ref-fixture", "course_name": "测试课程"}],
    "academic_items": [{"id": "assignment-fixture", "connection_id": "conn-fixture", "offering_id": "offering-fixture", "title": "练习一", "kind": "assignment", "status": "submitted"}],
}
TASK = "只读核对 connection_id=conn-fixture、course_ref=ref-fixture 这门课的正式作业状态，不创建Workflow或记忆。catalog="


def compact(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def schema_error(value, schema, path="arguments"):
    """Validate the subset used by the actual data_query/catalog tool schemas."""
    kind = schema.get("type")
    types = {"object": dict, "array": list, "string": str, "integer": int, "boolean": bool}
    if kind in types and (not isinstance(value, types[kind]) or kind == "integer" and isinstance(value, bool)):
        return path + " has the wrong type"
    if "enum" in schema and value not in schema["enum"]:
        return path + " must use a supported value"
    if isinstance(value, dict):
        for name in schema.get("required", []):
            if name not in value:
                return path + "." + name + " is required"
        props = schema.get("properties", {})
        for name, child in value.items():
            if name not in props and schema.get("additionalProperties") is False:
                return path + "." + name + " is not supported"
            error = schema_error(child, props.get(name, {}), path + "." + name)
            if error:
                return error
    if isinstance(value, list):
        if len(value) < schema.get("minItems", 0) or len(value) > schema.get("maxItems", float("inf")):
            return path + " exceeds array constraints"
        if schema.get("uniqueItems") and len({compact(v) for v in value}) != len(value):
            return path + " contains duplicates"
        for i, child in enumerate(value):
            error = schema_error(child, schema.get("items", {}), path + "[" + str(i) + "]")
            if error:
                return error
    if isinstance(value, str) and (len(value) < schema.get("minLength", 0) or len(value) > schema.get("maxLength", float("inf"))):
        return path + " exceeds string constraints"
    if isinstance(value, int) and not isinstance(value, bool) and (value < schema.get("minimum", -float("inf")) or value > schema.get("maximum", float("inf"))):
        return path + " exceeds numeric constraints"
    return None


def failure(code, message):
    return {"ok": False, "data": None, "error": {"code": code, "message": message, "retryable": False}}


def execute(name, args, specs, mode):
    if name not in specs:
        return failure("TOOL_NOT_FOUND", "tool is not available in this probe")
    if mode != "legacy":
        error = schema_error(args, specs[name]["parameters"])
        if error:
            return failure("INVALID_ARGUMENT", error + "; query did not execute")
    if name in ("data_list_tables", "data_describe_table"):
        catalog = [{"name": ds, "fields": [{"name": f, "type": "string"} for f in names]} for ds, names in FIELDS.items()]
        if name == "data_list_tables":
            return {"ok": True, "data": catalog, "error": None}
        entry = next((d for d in catalog if d["name"] == args.get("dataset")), None)
        return {"ok": True, "data": entry, "error": None} if entry else failure("PERMISSION_DENIED", "dataset is not authorized")
    dataset, names = args.get("dataset"), args.get("fields", [])
    if mode == "legacy":
        # Deliberately reproduce the old executor for the unchanged control case.
        filters = {f["field"]: f.get("value") for f in args.get("filters", []) if f.get("operator") == "eq"}
        if dataset not in FIELDS or not names or any(n not in FIELDS[dataset] for n in names):
            return failure("INVALID_ARGUMENT", "Use catalog dataset/fields")
        matched = dataset == "academic_offerings" and (filters.get("course_ref") == "ref-fixture" or filters.get("id") == "offering-fixture") or dataset == "academic_items" and filters.get("offering_id") == "offering-fixture"
        if not matched:
            return failure("INVALID_ARGUMENT", "Use the course relation returned by the parent query")
        return {"ok": True, "data": {"rows": [{n: ROWS[dataset][0][n] for n in names}], "has_more": False}, "error": None}
    if dataset not in FIELDS:
        return failure("PERMISSION_DENIED", "dataset is not authorized; available datasets: " + ",".join(FIELDS))
    for field in names + [f["field"] for f in args.get("filters", [])]:
        if field not in FIELDS[dataset]:
            return failure("PERMISSION_DENIED", "field is not authorized: " + field + "; see data_describe_table for " + dataset + ". This query did not execute")
    operations = {"eq": lambda x, y: x == y, "ne": lambda x, y: x != y, "lt": lambda x, y: x < y, "lte": lambda x, y: x <= y, "gt": lambda x, y: x > y, "gte": lambda x, y: x >= y, "contains": lambda x, y: y in x}
    matched = [r for r in ROWS[dataset] if all(operations[f["operator"]](r[f["field"]], f["value"]) for f in args.get("filters", []))]
    offset, limit = args.get("offset", 0), args.get("limit", 20)
    rows = matched[offset:offset + limit]
    data = {"rows": [{n: row[n] for n in names} for row in rows], "has_more": offset + len(rows) < len(matched)}
    if data["has_more"]:
        data["next_offset"] = offset + len(rows)
    return {"ok": True, "data": data, "error": None}


def run(args, key):
    root = pathlib.Path(__file__).resolve().parent
    entries = json.loads((root / "tool-catalog.json").read_text())
    names = ("data_query",) if args.mode == "legacy" else ("data_query", "data_describe_table", "data_list_tables")
    specs = {e["name"]: e for e in entries if e["name"] in names}
    tools = [{"type": "function", "function": {"name": name, "description": spec["description"], "parameters": spec["parameters"]}} for name, spec in specs.items()]
    prompt = (root.parent / "learner-agent/system-prompt.md").read_text()
    fields = FIELDS if args.mode == "legacy" else [{"name": ds, "fields": [{"name": f, "type": "string"} for f in names]} for ds, names in FIELDS.items()]
    messages = [{"role": "system", "content": prompt}, {"role": "user", "content": TASK + json.dumps(fields, ensure_ascii=False) + "。查询和是否继续由你决定，给出有据结论及限制。"}]
    directory = pathlib.Path(__import__("tempfile").mkdtemp(prefix="learner-hierarchy-" + args.mode + "-", dir=root.parents[1] / ".build"))
    directory.chmod(0o700)
    def save(name, value):
        path = directory / name
        with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2)
    summary = {"mode": args.mode, "model": args.model, "model_requests": 0, "trace": [], "usage": {}, "status": "budget_reached", "prompt_sha256": hashlib.sha256(prompt.encode()).hexdigest(), "fixture": "synthetic; no database or business writes", "output_directory": str(directory)}
    save("initial_context.json", {"tools": tools, "messages": messages})
    for turn in range(args.max_calls):
        body = {"model": args.model, "messages": messages, "tools": tools, "max_tokens": 1200, "stream": False}
        save(f"{turn+1:02d}-request.json", body)
        request = urllib.request.Request("https://api.deepseek.com/v1/chat/completions", data=compact(body).encode(), headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
        summary["model_requests"] += 1
        started = time.monotonic()
        try:
            with urllib.request.urlopen(request, timeout=60) as response:
                result = json.load(response)
        except urllib.error.HTTPError as error:
            error_body = error.read().decode(errors="replace").replace(key, "[redacted]")
            save(f"{turn+1:02d}-error.json", {"http_status": error.code, "body": error_body})
            summary["status"], summary["http_status"] = "provider_error", error.code
            break
        except (TimeoutError, OSError) as error:
            save(f"{turn+1:02d}-error.json", {"type": type(error).__name__})
            summary["status"] = "provider_connection_or_timeout"
            break
        save(f"{turn+1:02d}-response.json", result)
        summary["last_request_seconds"] = round(time.monotonic()-started, 3)
        for name, value in (result.get("usage") or {}).items():
            if isinstance(value, int):
                summary["usage"][name] = summary["usage"].get(name, 0) + value
        message = result["choices"][0]["message"]
        calls = message.get("tool_calls") or []
        if not calls:
            summary["status"], summary["answer"] = "finished", message.get("content")
            break
        messages.append(message)
        for call in calls:
            try:
                parameters = json.loads(call["function"]["arguments"])
                data = execute(call["function"]["name"], parameters, specs, args.mode)
            except (ValueError, KeyError, TypeError):
                parameters, data = {}, failure("INVALID_ARGUMENT", "invalid JSON tool arguments")
            summary["trace"].append({"tool": call["function"]["name"], "dataset": parameters.get("dataset"), "fields": parameters.get("fields"), "filters": parameters.get("filters"), "ok": data["ok"], "error": data.get("error")})
            save(f"{turn+1:02d}-{call['id']}-tool.json", {"call": call, "result": data})
            messages.append({"role": "tool", "tool_call_id": call["id"], "content": compact(data)})
        save("summary.json", summary)
    good = [t for t in summary["trace"] if t["tool"] == "data_query" and t["ok"]]
    summary["hierarchy_observed"] = len(good) >= 2 and good[0]["dataset"] == "academic_offerings" and good[1]["dataset"] == "academic_items"
    summary["tool_errors"] = sum(not t["ok"] for t in summary["trace"])
    summary["passed"] = summary["status"] == "finished" and summary["hierarchy_observed"] and not summary["tool_errors"]
    save("summary.json", summary)
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0 if summary["passed"] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("legacy", "catalog"), default="legacy")
    parser.add_argument("--model", choices=("deepseek-flash", "deepseek-v4-pro"), default="deepseek-flash")
    parser.add_argument("--max-calls", type=int, choices=range(1, 6), default=3)
    args = parser.parse_args()
    key = sys.stdin.readline().strip() if not sys.stdin.isatty() else getpass.getpass("API key: ")
    if not key:
        raise SystemExit("API key missing")
    return run(args, key)


if __name__ == "__main__":
    raise SystemExit(main())
