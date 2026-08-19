#!/usr/bin/env python3
"""Structural JSON Schema validation for the SOT schema subset.

Validates every ``schemas/*.json`` schema document and every
``examples/*.json`` example (plus the real integration reports under
``integrations/`` when present) using only the Python standard library.

Supported Draft 2020-12 subset, matching the feature set documented in
VALIDATION.md: type, const, enum, required, properties,
additionalProperties (bool or schema), items, pattern, minLength /
maxLength, minimum / maximum (inclusive and exclusive), minItems /
maxItems, uniqueItems, minProperties / maxProperties, allOf, oneOf,
anyOf, not, if / then / else, format=date-time (RFC 3339), and URN-based
cross-document ``$ref`` (with JSON pointer fragments) resolved against
the local schemas; siblings of ``$ref`` apply alongside the referenced
schema. Every invocation first runs a built-in self-test that exercises
each enforced keyword with valid and invalid instance cases (and static
schema-admission failures), so a regression in any assertion fails the
reproducible check itself.

Unknown assertion keywords, unsupported ``format`` values (only
``date-time`` and ``uri`` are implemented), and validator errors
(including invalid patterns) fail closed: each schema document is
statically admitted before instance validation, and runtime errors are
counted as document failures rather than silently skipped.
Non-assertion vocabulary (``$schema``, ``$id``, ``$defs``, ``title``,
``description``, ``$comment``, ``default``, ``examples``,
``deprecated``) is ignored.

Exits nonzero when any document fails validation or when a schema
document itself is structurally invalid as a schema; all failures are
reported before exiting.
"""

from __future__ import annotations

import datetime as _dt
import json
import operator as _op
import re
import sys
from pathlib import Path

_DATE_TIME = re.compile(
    r"^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$"
)

_URI = re.compile(r"\A[A-Za-z][A-Za-z0-9+.-]*:[^\s\x00-\x1f\x7f]+\Z")

_SUPPORTED_FORMATS = ("date-time", "uri")

_TYPES = {
    "object": dict,
    "array": list,
    "string": str,
    "boolean": bool,
    "null": type(None),
}

_NON_ASSERTION = {
    "$schema", "$id", "$comment", "$defs", "title", "description", "default",
    "examples", "deprecated", "readOnly", "writeOnly",
}

_KNOWN_KEYWORDS = _NON_ASSERTION | {
    "$ref", "type", "const", "enum", "format",
    "properties", "required", "additionalProperties",
    "items", "minItems", "maxItems", "uniqueItems",
    "pattern", "minLength", "maxLength",
    "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
    "minProperties", "maxProperties",
    "allOf", "oneOf", "anyOf", "not", "if", "then", "else",
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


def _equal(a: object, b: object) -> bool:
    # JSON Schema equality: numbers compare numerically (1 == 1.0), booleans
    # are never numbers, and containers compare element-wise recursively.
    a_num = isinstance(a, (int, float)) and not isinstance(a, bool)
    b_num = isinstance(b, (int, float)) and not isinstance(b, bool)
    if a_num and b_num:
        return a == b
    if a_num != b_num or type(a) is not type(b):
        if isinstance(a, bool) or isinstance(b, bool):
            return a is b
        if not (isinstance(a, (dict, list)) and isinstance(b, (dict, list))):
            return False
    if isinstance(a, dict) and isinstance(b, dict):
        return a.keys() == b.keys() and all(_equal(a[k], b[k]) for k in a)
    if isinstance(a, list) and isinstance(b, list):
        return len(a) == len(b) and all(_equal(x, y) for x, y in zip(a, b))
    return a == b


def _format_ok(value: str, fmt: str) -> bool:
    if fmt == "date-time":
        if not _DATE_TIME.match(value):
            return False
        try:
            _dt.datetime.fromisoformat(value.replace("Z", "+00:00").replace("z", "+00:00"))
        except ValueError:
            return False
        return True
    if fmt == "uri":
        # Absolute URI per RFC 3986: a scheme followed by a colon and a
        # non-empty remainder without spaces or control characters.
        return _URI.match(value) is not None
    raise ValueError(f"unsupported format keyword: {fmt}")


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

    def _check_keywords_supported(self, schema: dict, path: str) -> None:
        unknown = set(schema) - _KNOWN_KEYWORDS
        if unknown:
            raise ValueError(
                f"{path}: schema uses keywords this validator does not implement: "
                f"{sorted(unknown)}"
            )

    def check_schema_supported(self, schema: dict, path: str = "$") -> None:
        """Statically walk a schema document and fail closed on any keyword
        or format this validator cannot enforce, regardless of whether an
        instance value would ever reach the offending subschema. Skips the
        values of non-schema keywords (``default``, ``examples``, text
        metadata) because those are arbitrary instances, not subschemas.
        """
        self._check_keywords_supported(schema, path)
        if "format" in schema and schema["format"] not in _SUPPORTED_FORMATS:
            raise ValueError(f"{path}: unsupported format {schema['format']!r}")
        if "pattern" in schema:
            try:
                re.compile(schema["pattern"])
            except re.error as exc:
                raise ValueError(f"{path}: invalid pattern {schema['pattern']!r}: {exc}") from exc
        if "items" in schema and not isinstance(schema["items"], (dict, bool)):
            raise ValueError(f"{path}: items must be a schema or boolean in Draft 2020-12")
        schema_valued = ("properties", "$defs")
        for key in schema_valued:
            for name, sub in (schema.get(key) or {}).items():
                if not isinstance(sub, dict):
                    raise ValueError(
                        f"{path}.{key}.{name}: boolean subschemas are only "
                        f"supported for items and additionalProperties"
                    )
                self.check_schema_supported(sub, f"{path}.{key}.{name}")
        if isinstance(schema.get("additionalProperties"), dict):
            self.check_schema_supported(schema["additionalProperties"], f"{path}.additionalProperties")
        if isinstance(schema.get("items"), dict):
            self.check_schema_supported(schema["items"], f"{path}.items")
        for combinator in ("allOf", "anyOf", "oneOf"):
            for i, sub in enumerate(schema.get(combinator) or []):
                if not isinstance(sub, dict):
                    raise ValueError(
                        f"{path}.{combinator}[{i}]: boolean subschemas are only "
                        f"supported for items and additionalProperties"
                    )
                self.check_schema_supported(sub, f"{path}.{combinator}[{i}]")
        for key in ("not", "if", "then", "else"):
            if schema.get(key) is not None and not isinstance(schema[key], dict):
                raise ValueError(
                    f"{path}.{key}: boolean subschemas are only supported "
                    f"for items and additionalProperties"
                )
            if isinstance(schema.get(key), dict):
                self.check_schema_supported(schema[key], f"{path}.{key}")
        if "type" in schema:
            types = schema["type"]
            types = types if isinstance(types, list) else [types]
            unknown_types = [t for t in types if t not in _TYPES and t not in ("integer", "number")]
            if unknown_types:
                raise ValueError(f"{path}: unsupported type names {unknown_types}")

    def _numeric_ok(self, value: object, key: str, bound: object, path: str) -> bool:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            return True
        fail_when = {
            "minimum": _op.lt,
            "exclusiveMinimum": _op.le,
            "maximum": _op.gt,
            "exclusiveMaximum": _op.ge,
        }[key]
        if fail_when(value, bound):
            self.errors.append(f"{path}: {value} violates {key} bound {bound!r}")
            return False
        return True

    def validate(self, value: object, schema: dict, path: str = "$", root: dict | None = None) -> bool:
        if root is None:
            root = schema
        self._check_keywords_supported(schema, path)
        ok = True
        if "$ref" in schema:
            # Draft 2020-12: siblings of $ref apply alongside the referenced
            # schema, so evaluate the target first and keep checking siblings.
            target, target_doc = self.resolve_ref(schema["$ref"], root)
            if not self.validate(value, target, path, target_doc):
                return False
        if "const" in schema and not _equal(value, schema["const"]):
            self.errors.append(f"{path}: expected const {schema['const']!r}, got {value!r}")
            ok = False
        if "enum" in schema and not any(_equal(value, e) for e in schema["enum"]):
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
        if isinstance(value, str):
            if "pattern" in schema and re.search(schema["pattern"], value) is None:
                self.errors.append(f"{path}: {value!r} does not match pattern {schema['pattern']!r}")
                ok = False
            if "minLength" in schema and len(value) < schema["minLength"]:
                self.errors.append(f"{path}: length {len(value)} < minLength {schema['minLength']}")
                ok = False
            if "maxLength" in schema and len(value) > schema["maxLength"]:
                self.errors.append(f"{path}: length {len(value)} > maxLength {schema['maxLength']}")
                ok = False
        if isinstance(value, (int, float)):
            for key in ("minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"):
                if key in schema:
                    ok = self._numeric_ok(value, key, schema[key], path) and ok
        if isinstance(value, dict):
            if "minProperties" in schema and len(value) < schema["minProperties"]:
                self.errors.append(f"{path}: {len(value)} properties < minProperties {schema['minProperties']}")
                ok = False
            if "maxProperties" in schema and len(value) > schema["maxProperties"]:
                self.errors.append(f"{path}: {len(value)} properties > maxProperties {schema['maxProperties']}")
                ok = False
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
        if isinstance(value, list):
            if "minItems" in schema and len(value) < schema["minItems"]:
                self.errors.append(f"{path}: {len(value)} items < minItems {schema['minItems']}")
                ok = False
            if "maxItems" in schema and len(value) > schema["maxItems"]:
                self.errors.append(f"{path}: {len(value)} items > maxItems {schema['maxItems']}")
                ok = False
            if schema.get("uniqueItems") and len(value) >= 2:
                seen = []
                for i, item in enumerate(value):
                    if any(_equal(item, s) for s in seen):
                        self.errors.append(f"{path}[{i}]: duplicate item violates uniqueItems")
                        ok = False
                    else:
                        seen.append(item)
            if isinstance(schema.get("items"), dict):
                for i, item in enumerate(value):
                    ok = self.validate(item, schema["items"], f"{path}[{i}]", root) and ok
            elif schema.get("items") is False:
                if value:
                    self.errors.append(f"{path}: items schema false forbids any elements")
                    ok = False
        for combinator, expect in (("allOf", all), ("anyOf", any)):
            if combinator in schema:
                results = []
                for i, sub in enumerate(schema[combinator]):
                    v = Validator(self.schemas)
                    results.append(v.validate(value, sub, f"{path}.{combinator}[{i}]", root) and not v.errors)
                if not expect(results):
                    self.errors.append(f"{path}: fails {combinator}")
                    ok = False
        if "oneOf" in schema:
            matched = 0
            for i, sub in enumerate(schema["oneOf"]):
                v = Validator(self.schemas)
                if v.validate(value, sub, f"{path}.oneOf[{i}]", root) and not v.errors:
                    matched += 1
            if matched != 1:
                self.errors.append(f"{path}: matched {matched} oneOf branches, expected exactly 1")
                ok = False
        if "not" in schema:
            v = Validator(self.schemas)
            if v.validate(value, schema["not"], f"{path}.not", root) and not v.errors:
                self.errors.append(f"{path}: matches forbidden 'not' schema")
                ok = False
        if "if" in schema:
            v = Validator(self.schemas)
            branch_taken = v.validate(value, schema["if"], f"{path}.if", root) and not v.errors
            branch_key = "then" if branch_taken else "else"
            if branch_key in schema:
                ok = self.validate(value, schema[branch_key], f"{path}.{branch_key}", root) and ok
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
        Validator({}).check_schema_supported(doc, path.name)
        schemas[sid] = doc
    return schemas


def _self_test() -> bool:
    """Exercise every enforced keyword with valid and invalid instance cases.

    Runs on every invocation so a regression in any assertion (for example a
    reversed comparison bound) fails the reproducible check itself, not just
    unknown future documents.
    """
    cases: list[tuple[str, dict, object, bool]] = [
        ("type-ok", {"type": "integer"}, 3, True),
        ("type-bad", {"type": "integer"}, "3", False),
        ("const-ok", {"const": [1, 2]}, [1, 2], True),
        ("const-bad", {"const": [1, 2]}, [2, 1], False),
        ("enum-ok", {"enum": ["a", "b"]}, "b", True),
        ("enum-bad", {"enum": ["a", "b"]}, "c", False),
        ("bool-not-int", {"type": "integer"}, True, False),
        ("pattern-ok", {"pattern": "^sha256:[0-9a-f]{8}$"}, "sha256:deadbeef", True),
        ("pattern-bad", {"pattern": "^sha256:[0-9a-f]{8}$"}, "sha256:DEADBEEF", False),
        ("minLength-bad", {"minLength": 3}, "ab", False),
        ("maxLength-bad", {"maxLength": 3}, "abcd", False),
        ("minimum-ok", {"minimum": 1}, 1, True),
        ("minimum-bad", {"minimum": 1}, 0, False),
        ("maximum-bad", {"maximum": 10}, 11, False),
        ("exclusiveMinimum-ok", {"exclusiveMinimum": 1}, 2, True),
        ("exclusiveMinimum-bad", {"exclusiveMinimum": 1}, 1, False),
        ("exclusiveMaximum-ok", {"exclusiveMaximum": 10}, 9, True),
        ("exclusiveMaximum-bad", {"exclusiveMaximum": 10}, 10, False),
        ("minItems-bad", {"minItems": 2}, [1], False),
        ("maxItems-bad", {"maxItems": 1}, [1, 2], False),
        ("uniqueItems-ok", {"uniqueItems": True}, [1, 2, "1"], True),
        ("uniqueItems-bad", {"uniqueItems": True}, [1, 1], False),
        ("uniqueItems-bool-int", {"uniqueItems": True}, [1, True], True),
        ("minProperties-bad", {"minProperties": 2}, {"a": 1}, False),
        ("maxProperties-bad", {"maxProperties": 1}, {"a": 1, "b": 2}, False),
        ("required-bad", {"required": ["a"]}, {}, False),
        ("additional-bad", {"properties": {"a": {}}, "additionalProperties": False}, {"a": 1, "b": 2}, False),
        ("items-bad", {"items": {"type": "string"}}, ["a", 1], False),
        ("allOf-bad", {"allOf": [{"minimum": 0}, {"maximum": 5}]}, 7, False),
        ("anyOf-ok", {"anyOf": [{"type": "string"}, {"type": "integer"}]}, 7, True),
        ("anyOf-bad", {"anyOf": [{"type": "string"}, {"type": "integer"}]}, 1.5, False),
        ("oneOf-ok", {"oneOf": [{"type": "string"}, {"type": "integer"}]}, "a", True),
        ("oneOf-two-bad", {"oneOf": [{"type": "string"}, {"minLength": 1}]}, "ab", False),
        ("not-bad", {"not": {"type": "null"}}, None, False),
        ("if-then-bad", {"if": {"properties": {"a": {"const": 1}}}, "then": {"required": ["b"]}}, {"a": 1}, False),
        ("if-else-ok", {"if": {"properties": {"a": {"const": 1}}}, "else": {"required": ["c"]}}, {"a": 2, "c": 3}, True),
        ("if-else-bad", {"if": {"properties": {"a": {"const": 1}}}, "else": {"required": ["c"]}}, {"a": 2}, False),
        ("format-ok", {"format": "date-time"}, "2026-08-19T21:25:24+09:00", True),
        ("format-bad", {"format": "date-time"}, "2026-08-19", False),
        ("uri-ok", {"format": "uri"}, "https://example.test/hook", True),
        ("uri-no-scheme-bad", {"format": "uri"}, "//example.test/hook", False),
        ("uri-empty-rest-bad", {"format": "uri"}, "https:", False),
        ("uri-space-bad", {"format": "uri"}, "https://exa mple.test", False),
        ("const-numeric-equal-ok", {"const": 1}, 1.0, True),
        ("const-numeric-equal-bad", {"const": 1}, 2, False),
        ("const-bool-not-one-bad", {"const": 1}, True, False),
        ("uniqueItems-numeric-bad", {"uniqueItems": True}, [1, 1.0], False),
        ("items-false-ok", {"items": False}, [], True),
        ("items-false-bad", {"items": False}, [1], False),
        ("items-true-ok", {"items": True}, [1, "a", None], True),
    ]
    # $ref resolution and Draft 2020-12 sibling application.
    ref_schemas = {
        "urn:jjukkumi:selftest:v1": {
            "$id": "urn:jjukkumi:selftest:v1",
            "$defs": {"pos": {"minimum": 0}, "small": {"maximum": 10}},
            "type": "object",
        }
    }
    cases += [
        ("ref-cross-doc-ok", {"$ref": "urn:jjukkumi:selftest:v1#/$defs/pos"}, 5, True),
        ("ref-cross-doc-bad", {"$ref": "urn:jjukkumi:selftest:v1#/$defs/pos"}, -1, False),
        (
            "ref-sibling-applies-ok",
            {"$ref": "urn:jjukkumi:selftest:v1#/$defs/pos", "maximum": 10},
            5,
            True,
        ),
        (
            "ref-sibling-applies-bad",
            {"$ref": "urn:jjukkumi:selftest:v1#/$defs/pos", "maximum": 10},
            99,
            False,
        ),
        ("deep-numeric-equal-ok", {"const": [[1, {"x": 2}]]}, [[1.0, {"x": 2.0}]], True),
        ("deep-numeric-unequal-bad", {"const": [[1]]}, [[2]], False),
    ]
    case_schemas = {
        "ref-cross-doc-ok": ref_schemas,
        "ref-cross-doc-bad": ref_schemas,
        "ref-sibling-applies-ok": ref_schemas,
        "ref-sibling-applies-bad": ref_schemas,
    }
    passed = True
    for name, schema, value, expect_valid in cases:
        v = Validator(case_schemas.get(name, {}))
        try:
            valid = v.validate(value, schema) and not v.errors
        except ValueError:
            valid = False
        if valid != expect_valid:
            print(f"self-test FAIL {name}: expected_valid={expect_valid} errors={v.errors}", file=sys.stderr)
            passed = False
    # Static schema admission: unsupported keywords/formats fail regardless
    # of instance reachability.
    static_count = 0
    for name, bad_schema in [
        ("static-unknown-keyword", {"properties": {"a": {"maxLength": 3, "contentEncoding": "base64"}}}),
        ("static-unsupported-format", {"properties": {"a": {"format": "uuid"}}}),
        ("static-invalid-pattern", {"properties": {"a": {"pattern": "([unclosed"}}}),
        ("static-items-not-schema", {"items": [{"type": "string"}]}),
        ("static-bool-property-schema", {"properties": {"a": False}}),
        ("static-bool-combinator-schema", {"allOf": [True]}),
        ("static-unknown-type-name", {"type": ["string", "gizmo"]}),
    ]:
        static_count += 1
        try:
            Validator({}).check_schema_supported(bad_schema)
            print(f"self-test FAIL {name}: accepted unsupported schema", file=sys.stderr)
            passed = False
        except ValueError:
            pass
    if not passed:
        return False
    print(f"self-test: {len(cases)} keyword cases and {static_count} static-admission cases pass")
    return True


def main() -> int:
    if not _self_test():
        return 1
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
        schema_errors: list[str] = []
        for schema in schemas.values():
            v = Validator(schemas)
            try:
                if v.validate(doc, schema) and not v.errors:
                    matched += 1
            except (ValueError, TypeError, re.error) as exc:
                schema_errors.append(f"{type(exc).__name__}: {exc}")
        if schema_errors:
            print(f"FAIL {example.relative_to(root)}: validator cannot check a schema:")
            for err in schema_errors:
                print(f"     {err}")
            failures += 1
        elif matched == 0:
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
        try:
            if v.validate(doc, schemas[sid]) and not v.errors:
                print(f"ok   {rel} (against {sid})")
            else:
                print(f"FAIL {rel}:")
                for err in v.errors:
                    print(f"     {err}")
                failures += 1
        except (ValueError, TypeError, re.error) as exc:
            print(f"FAIL {rel}: validator cannot check the schema: {type(exc).__name__}: {exc}")
            failures += 1

    if failures:
        print(f"{failures} document(s) failed validation", file=sys.stderr)
        return 1
    print("all documents valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
