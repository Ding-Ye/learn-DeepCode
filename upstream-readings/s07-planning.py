# =============================================================================
#  Annotated extract from HKUDS/DeepCode @ b9ece6035ea3f3582e6c503c517206b23c09ad09
#  File: workflows/planning_runtime.py  (L1-L263, abridged)
#  License: MIT (Copyright 2025 Data Intelligence Lab @ HKU)
# =============================================================================
"""Planning-phase persistence and validation helpers."""

# >>> s07: Three concerns are mixed in this file — paths, JSON I/O, plan
#     validation. The Go port keeps them in three files (paths.go, atomic.go,
#     validate.go) so each is independently grep-able.
from __future__ import annotations

import json
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import yaml

# >>> s07: This tuple is the contract. Both Python and Go ports must keep
#     these names verbatim — they appear in plan-template prompts elsewhere
#     in upstream and any rename here is a breaking change for every plan
#     ever written. Go: validate.go RequiredPlanSections (same order).
REQUIRED_PLAN_SECTIONS = (
    "file_structure",
    "implementation_components",
    "validation_approach",
    "environment_setup",
    "implementation_strategy",
)


def planning_paths(paper_dir: str | Path) -> dict[str, Path]:
    # >>> s07: Same shape as Go's PlanningPaths(taskDir). The "paper_dir"
    #     name is upstream legacy — semantically it's the task directory.
    root = Path(paper_dir)
    return {
        "checkpoint": root / "planning_checkpoint.json",
        "attempts": root / "planning_attempts.jsonl",
        "meta": root / "planning_result_meta.json",
    }


def write_json(path: str | Path, payload: dict[str, Any]) -> None:
    # >>> s07: This is the atomic-write primitive. Sequence: write to a
    #     ".tmp" sibling, then `tmp.replace(target)` (POSIX rename, atomic
    #     within a directory). The Go port adds an explicit fsync between
    #     write and rename — Python relies on the OS doing it on close, but
    #     a hard crash between close() and replace() can lose data. Go's
    #     atomic.go is a slight strengthening, not a port bug.
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp = target.with_suffix(target.suffix + ".tmp")
    tmp.write_text(
        json.dumps(payload, ensure_ascii=False, indent=2, default=str),
        encoding="utf-8",
    )
    tmp.replace(target)


def append_jsonl(path: str | Path, payload: dict[str, Any]) -> None:
    # >>> s07: Python relies on the GIL for line atomicity within one
    #     process. Go has no GIL, so jsonl.go uses a per-path sync.Mutex.
    #     Neither approach protects against multiple OS processes writing
    #     the same file — for that, both languages need flock(2). See
    #     Appendix B exercise #4 in the curriculum plan.
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(payload, ensure_ascii=False, default=str))
        handle.write("\n")


def validate_plan_text(text: str) -> dict[str, Any]:
    """Validate the reproduction plan shape without requiring perfect YAML."""
    # >>> s07: The "loose" check — case-insensitive substring of "section:"
    #     in the lower-cased text. Upstream returns a rich dict; the Go
    #     port returns just []string of MISSING sections. The richer dict
    #     is unused outside this file in practice — every caller only reads
    #     missing_sections — so simplifying is safe.
    candidate = re.sub(r"```(?:yaml|yml)?\s*(.*?)```", r"\1", text or "",
                       flags=re.IGNORECASE | re.DOTALL).strip()
    lower_text = (text or "").lower()
    string_missing = [
        section for section in REQUIRED_PLAN_SECTIONS
        if f"{section}:" not in lower_text
    ]
    # ... (omitted: the full upstream function then tries yaml.safe_load and
    #     re-checks against the parsed structure. The Go port skips this
    #     because (a) the substring check is good enough in practice, and
    #     (b) Go's stdlib has no YAML — pulling in a YAML dependency just
    #     for validation would be lopsided.)
    return {"missing_sections": string_missing, "valid": not string_missing}


def is_existing_plan_usable(
    initial_plan_path: str | Path,
    *,
    paper_dir: str | Path,
    min_chars: int = 500,
) -> tuple[bool, dict[str, Any]]:
    # >>> s07: Upstream re-reads initial_plan.txt and re-validates its body.
    #     The Go port's IsExistingPlanUsable is intentionally less paranoid:
    #     it just checks meta.status=="success" + checkpoint exists. The
    #     full body re-read belongs in s10 (where the orchestrator decides
    #     to consume the plan), not in the runtime helper.
    path = Path(initial_plan_path)
    meta = json.loads(Path(planning_paths(paper_dir)["meta"]).read_text())
    if not path.exists():
        return False, {"reason": "missing_initial_plan", "meta": meta}
    text = path.read_text(encoding="utf-8")
    validation = validate_plan_text(text)
    reusable = len(text.strip()) >= min_chars and bool(validation["valid"])
    return reusable, {"reason": "usable" if reusable else "invalid", "meta": meta}
