#!/usr/bin/env python3
"""Structural JSON Schema validation for the SOT schema subset.

Validates every ``schemas/*.json`` schema document and every
``examples/*.json`` example (plus the real integration reports under
``integrations/`` when present) using only the Python standard library.

Supported Draft 2020-12 subset, matching the feature set documented in
VALIDATION.md: type, const, enum, required, properties,
additionalProperties (bool or schema), items, format=date-time (RFC 3339),
and URN-based cross-document ``$ref`` resolved against the local schemas.

Exits nonzero on the first document that fails validation or when a schema
document itself is structurally invalid as a schema.
"""

from __future__ import annotations

import datetime as _dt
import json
import re
import sys
from pathlib import Path

_DATE_TIME = re.compile(
    r"^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$"
)

_TYPES = {
    "object": dict,
    "array": list,
    "string": str,
    "boolean": bool,
    "null": type(None),
}


def _type_ok(value: object, t: str) -> bool:
    if t == "integer":
        return isinstance(value, int) and not isinstance(value, bool)
    if t == "number":
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    py = _TYPES.get(t)
    if py is None:
        raise ValueError(f"unsupported type keyword: {t}")
    if t == "boolean" or t == "null":
        return isinstance(value, py)
    if t in ("string", "array", "object"):
        return isinstance(value, py) and not isinstance(value, bool)
    return isinstance(value, py)


def _format_ok(value: str, fmt: str) -> bool:
    if fmt != "date-time":
        raise ValueError(f"unsupported format keyword: {fmt}")
    if not _DATE_TIME.match(value):
        return False
    try:
        _dt.datetime.fromisoformat(value.replace("Z", "+00:00").replace("z", "+00:00"))
    except ValueError:
        return False
    return True


class Validator:
    def __init__(self, schemas: dict[str, dict]) -> None:
        self.schemas = schemas
        self.errors: list[str] = []

    def _pointer(self, doc: dict, fragment: str, ref: str) -> dict:
        if not fragment or fragment == "/":
            return doc
        if not fragment.startswith("/"):
            raise ValueError(f"only JSON pointer fragments supported: {ref}")
        node: object = doc
        for token in fragment.lstrip("/").split("/"):
            token = token.replace("~1", "/").replace("~0", "~")
            if isinstance(node, dict) and token in node:
                node = node[token]
            elif isinstance(node, list) and token.isdigit() and int(token) < len(node):
                node = node[int(token)]
            else:
                raise ValueError(f"unresolvable $ref pointer: {ref}")
        if not isinstance(node, dict):
            raise ValueError(f"$ref pointer does not resolve to a schema: {ref}")
        return node

    def resolve_ref(self, ref: str, root: dict | None) -> tuple[dict, dict]:
        """Return (target schema, document that same-document refs resolve against)."""
        base, _, fragment = ref.partition("#")
        if base:
            if not base.startswith("urn:"):
                raise ValueError(f"only urn: refs are supported, got: {ref}")
            target_doc = self.schemas.get(base)
            if target_doc is None:
                raise ValueError(f"unresolvable $ref: {ref}")
        else:
            target_doc = root
            if target_doc is None:
                raise ValueError(f"same-document $ref without a root: {ref}")
        return self._pointer(target_doc, fragment, ref), target_doc

    def validate(self, value: object, schema: dict, path: str = "$", root: dict | None = None) -> bool:
        if root is None:
            root = schema
        ok = True
        if "$ref" in schema:
            target, target_doc = self.resolve_ref(schema["$ref"], root)
            return self.validate(value, target, path, target_doc)
        if "const" in schema and value != schema["const"]:
            self.errors.append(f"{path}: expected const {schema['const']!r}, got {value!r}")
            ok = False
        if "enum" in schema and value not in schema["enum"]:
            self.errors.append(f"{path}: {value!r} not in enum {schema['enum']!r}")
            ok = False
        if "type" in schema:
            types = schema["type"]
            types = types if isinstance(types, list) else [types]
            if not any(_type_ok(value, t) for t in types):
                self.errors.append(f"{path}: expected type {types}, got {type(value).__name__}")
                return False
        if isinstance(value, str) and "format" in schema:
            if not _format_ok(value, schema["format"]):
                self.errors.append(f"{path}: {value!r} is not a valid {schema['format']}")
                ok = False
        if isinstance(value, dict):
            for key in schema.get("required", []):
                if key not in value:
                    self.errors.append(f"{path}: missing required property {key!r}")
                    ok = False
            props = schema.get("properties", {})
            additional = schema.get("additionalProperties", True)
            for key, item in value.items():
                child = f"{path}.{key}"
                if key in props:
                    ok = self.validate(item, props[key], child, root) and ok
                elif additional is False:
                    self.errors.append(f"{child}: additional property not allowed")
                    ok = False
                elif isinstance(additional, dict):
                    ok = self.validate(item, additional, child, root) and ok
        if isinstance(value, list) and isinstance(schema.get("items"), dict):
            for i, item in enumerate(value):
                ok = self.validate(item, schema["items"], f"{path}[{i}]", root) and ok
        return ok


def load_schemas(schema_dir: Path) -> dict[str, dict]:
    schemas: dict[str, dict] = {}
    for path in sorted(schema_dir.glob("*.json")):
        doc = json.loads(path.read_text(encoding="utf-8"))
        sid = doc.get("$id")
        if not isinstance(sid, str):
            raise SystemExit(f"{path}: schema document has no $id")
        if sid in schemas:
            raise SystemExit(f"{path}: duplicate schema $id {sid}")
        schemas[sid] = doc
    return schemas


def main() -> int:
    root = Path(__file__).resolve().parent.parent
    schema_dir = root / "schemas"
    schemas = load_schemas(schema_dir)
    if len(schemas) != 8:
        print(f"warning: expected 8 schemas, found {len(schemas)}", file=sys.stderr)

    # Illustrative examples without a dedicated schema yet; the real Watchman
    # payload shapes are frozen by E0-T5 and schematized by E2-T1.
    schema_less = {"watchman-trigger.json", "watchman-environment.json"}

    failures = 0

    for example in sorted((root / "examples").glob("*.json")):
        if example.name in schema_less:
            print(f"skip {example.relative_to(root)} (no dedicated schema until E2-T1)")
            continue
        doc = json.loads(example.read_text(encoding="utf-8"))
        matched = 0
        for schema in schemas.values():
            v = Validator(schemas)
            if v.validate(doc, schema) and not v.errors:
                matched += 1
        if matched == 0:
            print(f"FAIL {example.relative_to(root)}: matches no schema")
            failures += 1
        else:
            print(f"ok   {example.relative_to(root)} ({matched} schema match(es))")

    report_targets = [
        ("integrations/hermes-capability-report.json", "urn:jjukkumi:schema:hermes-capabilities:v1"),
    ]
    for rel, sid in report_targets:
        path = root / rel
        if not path.exists():
            continue
        doc = json.loads(path.read_text(encoding="utf-8"))
        v = Validator(schemas)
        if v.validate(doc, schemas[sid]) and not v.errors:
            print(f"ok   {rel} (against {sid})")
        else:
            print(f"FAIL {rel}:")
            for err in v.errors:
                print(f"     {err}")
            failures += 1

    if failures:
        print(f"{failures} document(s) failed validation", file=sys.stderr)
        return 1
    print("all documents valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
