#!/usr/bin/env python3
"""Generate docs/specs/traceability-matrix.md from the roadmap and required-spec.

The citing-task column is derived mechanically from each roadmap task's
Requirements section. Range citations (``SRC-001`` through ``SRC-003``) and
wildcards (``BND-*``) are expanded against required-spec.md. Do not edit the
generated table by hand; rerun this script instead:

    python3 scripts/generate-traceability.py

Run from the docs/ directory. The script fails if a cited requirement ID does
not exist in required-spec.md, so forward references cannot drift silently.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

DOCS = Path(__file__).resolve().parent.parent
SPEC = DOCS / "specs/required-spec.md"
ROADMAP = DOCS / "roadmap/roadmap.md"
OUT = DOCS / "specs/traceability-matrix.md"

# Release-gate tasks per group. Manually curated; these are verification
# endpoints, not citation coverage.
FINAL_VERIFICATION = {
    "BND": "E6-T4, E13-T4, E15-T4, E17-T2, E17-T3",
    "SCP": "E6-T3, E6-T4, E17-T3",
    "SRC": "E2-T5, E6-T3, E10-T3, E13-T4, E17-T3",
    "PTH": "E2-T5, E5-T5, E10-T3, E13-T4, E17-T3",
    "DAT": "E3-T5, E12-T4, E13-T4, E17-T3",
    "POL": "E5-T5, E17-T3",
    "DUR": "E3-T5, E4-T5, E10-T3, E13-T1, E13-T4, E14-T3, E15-T4, E16-T4, E17-T3",
    "CON": "E5-T5, E12-T4, E13-T4, E15-T4, E17-T3",
    "HER": "E4-T5, E6-T4, E11-T4, E13-T4, E15-T4, E17-T3",
    "WHK": "E6-T4, E17-T3",
    "FBK": "E5-T5, E12-T4, E13-T3, E13-T4, E17-T3",
    "CLI": "E6-T4, E11-T4, E13-T3, E13-T4, E14-T3, E16-T4, E17-T3",
    "SEC": "E5-T5, E6-T4, E11-T4, E13-T2, E13-T4, E17-T2, E17-T3",
    "OPS": "E6-T4, E10-T3, E11-T4, E13-T4, E14-T3, E16-T4, E17-T3",
    "TST": "E6-T4, E13-T4, E14-T3, E15-T4, E16-T4, E17-T2, E17-T3",
    "FAN": "E12-T4, E13-T4, E17-T3",
    "NTF": "E13-T3, E13-T4, E16-T4, E17-T3",
}

EPIC_CONTRIBUTION = """\
| Epic | Required-state increment |
|---|---|
| E0 | Freezes product boundaries and proves which public Hermes capabilities actually exist. |
| E1 | Creates a testable Go foundation, validated config, stable identity primitives, and durable schema. |
| E2 | Converts Watchman input into a safe deterministic plan without agent side effects. |
| E3 | Makes planned work durable, serializable, retryable, and recoverable. |
| E4 | Delivers one effective durable Hermes Kanban task and observes acceptance safely. |
| E5 | Controls recursive edits, dirty generations, protected cases, and reconciliation. |
| E6 | Adds explicit webhook delivery, operational tooling, packaging, and release proof. |
| E7 | Remediates the first post-release MVP compliance review. |
| E8 | Closes the second review's lifecycle, gate, and documentation gaps. |
| E9 | Hardens deferred contracts and releases through v0.1.4. |
| E10 | Fences reconciliation and binds Watchman to the configured subtree. |
| E11 | Adds capability-driven Hermes preflight and guided disabled setup. |
| E12 | Adds aggregate events and independent destination lifecycle. |
| E13 | Adds durable notifications, operational proof, and the v0.1.5 release. |
| E14 | Makes guided setup route-correct and safely establishes a disabled baseline. |
| E15 | Makes target mutex optional while enforcing explicit local serialization groups. |
| E16 | Adds bounded post-commit and scheduled progress for the durable notification outbox. |
| E17 | Reconciles documentation and real evidence, then releases v0.1.6 reproducibly. |"""


def load_spec_groups() -> dict[str, list[int]]:
    groups: dict[str, list[int]] = {}
    for match in re.finditer(r"^\| ([A-Z]{3})-(\d{3}) \|", SPEC.read_text(), re.M):
        group, number = match.group(1), int(match.group(2))
        groups.setdefault(group, []).append(number)
    if not groups:
        sys.exit("required-spec.md yielded no requirement IDs")
    return groups


def load_task_requirements() -> dict[str, str]:
    tasks: dict[str, str] = {}
    current: str | None = None
    section: str | None = None
    for line in ROADMAP.read_text().splitlines():
        task_match = re.match(r"^## (E\d+-T\d+):", line)
        if task_match:
            current = task_match.group(1)
            section = None
            continue
        if current is None:
            continue
        if line.startswith("### "):
            section = line.removeprefix("### ").strip()
            tasks.setdefault(current, "")
            continue
        if section == "Requirements":
            tasks[current] += line + "\n"
    return tasks


def expand_citations(text: str, groups: dict[str, list[int]]) -> set[str]:
    ids: set[str] = set()

    # Drop cross-group release-gate blankets (E6-T4's "All `BND-*` through
    # `TST-*` requirements"); they are documented as the Final verification
    # column instead of per-group citations.
    text = re.sub(r"All `([A-Z]{3})-\*` through `([A-Z]{3})-\*` requirements?", "", text)

    # ``XXX-001`` through ``XXX-003`` (same group only; cross-group blankets
    # such as E6-T4's "All `BND-*` through `TST-*`" are intentionally not
    # expanded and are documented as the release gate instead).
    for match in re.finditer(r"`([A-Z]{3})-(\d{3})` through `([A-Z]{3})-(\d{3})`", text):
        left_group, left, right_group, right = match.groups()
        if left_group != right_group:
            continue
        ids.update(f"{left_group}-{n:03d}" for n in range(int(left), int(right) + 1))

    for match in re.finditer(r"\b([A-Z]{3})-(\d{3})\b", text):
        ids.add(f"{match.group(1)}-{match.group(2)}")

    for match in re.finditer(r"\b([A-Z]{3})-\*", text):
        group = match.group(1)
        ids.update(f"{group}-{n:03d}" for n in groups.get(group, []))

    return ids


def task_sort_key(task: str) -> tuple[int, int]:
    epic, number = task.removeprefix("E").split("-T")
    return int(epic), int(number)


def main() -> None:
    groups = load_spec_groups()
    known = {f"{g}-{n:03d}" for g, numbers in groups.items() for n in numbers}

    citations: dict[str, set[str]] = {}
    for task, text in load_task_requirements().items():
        expanded = expand_citations(text, groups)
        unknown = expanded - known
        if unknown:
            sys.exit(f"{task} cites unknown requirement IDs: {sorted(unknown)}")
        citations[task] = expanded

    lines = [
        "# Requirement Traceability Matrix",
        "",
        "> Generated by `scripts/generate-traceability.py` from the Requirements",
        "> sections of `docs/roadmap/roadmap.md`. Do not edit the citing-task",
        "> column by hand; rerun the script after changing the roadmap.",
        "",
        "| Requirement group | Citing tasks | Final verification |",
        "|---|---|---|",
    ]
    for group in sorted(groups):
        citing = sorted((t for t, ids in citations.items() if any(i.startswith(group + "-") for i in ids)), key=task_sort_key)
        lines.append(f"| `{group}-*` | {', '.join(citing)} | {FINAL_VERIFICATION[group]} |")

    lines += [
        "",
        "E6-T4 additionally cites every requirement group as the release gate",
        "(\"All `BND-*` through `WHK-*` requirements\"); that blanket citation is",
        "covered by the Final verification column and is not expanded above.",
        "",
        "## Epic Contribution to Required State",
        "",
        EPIC_CONTRIBUTION,
        "",
    ]

    OUT.write_text("\n".join(lines))
    task_count = len(citations)
    print(f"wrote {OUT.relative_to(DOCS)} ({task_count} tasks, {len(groups)} groups)")


if __name__ == "__main__":
    main()
