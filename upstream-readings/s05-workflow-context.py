# =============================================================================
#  Annotated extract from HKUDS/DeepCode @ b9ece6035ea3f3582e6c503c517206b23c09ad09
#  File: workflows/workflow_context.py  (L1-L168, abridged)
#  License: MIT (Copyright 2025 Data Intelligence Lab @ HKU)
# =============================================================================
"""Single-source-of-truth context object for the multi-agent research pipeline."""

# >>> s05: Python uses pathlib.Path everywhere. Go uses path/filepath
#     functions on plain strings — the standard idiom in Go is "string +
#     filepath.Join", not a dedicated Path type. Either is fine; the key
#     property is "never concatenate with raw '/'", which both languages
#     enforce by convention.
from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Literal

# >>> s05: Python's `Literal["pdf", ...]` becomes a Go `type InputKind string`
#     plus a set of typed constants (InputKindPDF, InputKindURL, ...). Go's
#     compiler won't catch a stray `InputKind("nope")` at compile time, but
#     callers using the constants get autocompletion + grep-ability.
InputKind = Literal["pdf", "md", "docx", "txt", "html", "url"]


# >>> s05: This extension table maps to s05/prepare.go's `extensionToKind`.
#     We keep the same keys verbatim so behaviour matches (note that
#     ".markdown" → "md" and ".doc" → "docx" — both are easy to forget).
EXTENSION_TO_KIND: dict[str, InputKind] = {
    ".pdf": "pdf",
    ".md": "md",
    ".markdown": "md",
    ".docx": "docx",
    ".doc": "docx",
    ".txt": "txt",
    ".html": "html",
    ".htm": "html",
}


# >>> s05: Upstream has a TaskKind axis (paper2code / chat2code / text2web).
#     We INTENTIONALLY drop this in s05 because the Go learning repo only
#     ports paper2code. Adding TaskKind would balloon Prepare() with a
#     prefix-mapping table that none of the later sessions actually use.
#     If a future chapter needs it, it would slot in as Options.TaskKind.
TaskKind = Literal["paper2code", "chat2code", "text2web"]
TASK_KIND_PREFIX: dict[TaskKind, str] = {
    "paper2code": "paper",
    "chat2code": "chat",
    "text2web": "web",
}
TASKS_DIRNAME = "tasks"


# =============================================================================
# >>> s05: WorkflowContext — our Go counterpart is `WorkflowContext` in
#     context.go. Same logical fields, different mechanism for immutability:
#       - Python uses @dataclass(slots=True) — slots saves memory, but the
#         instance is still mutable (no frozen=True here).
#       - Go uses unexported fields + value-type semantics. Every caller
#         receives a copy; no exported setter exists; the compiler refuses
#         `ctx.taskID = "x"` from outside the package. This is structurally
#         immutable in a way the upstream class is not.
#     Note: upstream is NOT frozen — Phase 2/3 actually mutate paper_path /
#     standardized_text. Our Go port does NOT keep those fields; we will
#     add a separate Phase2Output value if/when we port Phase 2.
# =============================================================================
@dataclass(slots=True)
class WorkflowContext:
    """Everything the pipeline needs to know about one task."""

    # >>> s05: Maps to WorkflowContext.taskID (private).
    task_id: str
    # >>> s05: Maps to WorkflowContext.inputSource (private). We keep the
    #     verbatim original input rather than normalising — Go has no
    #     pathlib.Path so we don't auto-resolve to absolute.
    input_source: str
    input_kind: InputKind
    # >>> s05: Upstream uses Path; Go uses string + filepath.Join.
    workspace_root: Path
    task_dir: Path
    # >>> s05: enable_indexing / task_kind / skip_research_analysis are
    #     dropped — they are pipeline routing flags, not data the context
    #     itself needs. Our Go port keeps Context as 5 fields total.
    enable_indexing: bool
    task_kind: TaskKind = "paper2code"
    skip_research_analysis: bool = False
    # >>> s05: paper_path / paper_md_path / standardized_text are mutated
    #     by Phase 2/3. We deliberately omit them so our context is fully
    #     immutable; a future phase port would carry these in its own
    #     output value.
    paper_path: Path | None = None
    paper_md_path: Path | None = None
    standardized_text: str | None = None

    # >>> s05: These @property accessors map 1:1 to value-receiver methods
    #     in s05/paths.go: ReferencePath, InitialPlanPath, etc.
    #     Upstream uses .txt; we use .md throughout (the rest of
    #     learn-DeepCode is markdown-first).
    @property
    def reference_path(self) -> Path:
        return self.task_dir / "reference.txt"

    @property
    def initial_plan_path(self) -> Path:
        return self.task_dir / "initial_plan.txt"

    @property
    def implementation_report_path(self) -> Path:
        return self.task_dir / "code_implementation_report.txt"

    # >>> s05: to_dir_info is a legacy bridge — older phases consume a
    #     stringly-typed dict. Go callers use the accessor methods directly,
    #     so we drop this entirely. If you ever need a serialisable form,
    #     add a `MarshalJSON` to WorkflowContext.
    def to_dir_info(self) -> dict[str, Any]:
        return {
            "paper_dir": str(self.task_dir),
            "standardized_text": self.standardized_text,
            "reference_path": str(self.reference_path),
            "initial_plan_path": str(self.initial_plan_path),
            "implementation_report_path": str(self.implementation_report_path),
            "workspace_dir": str(self.workspace_root),
        }


# =============================================================================
# >>> s05: resolve_workspace_root — three-step priority resolution.
#     Our Go port simplifies to two steps: opts.WorkspaceRoot, else
#     $HOME/.deepcode-learn. We push env-var interpolation to s03 (config
#     loader) so s05 can stay zero-dependency.
# =============================================================================
def resolve_workspace_root(yaml_root: str | None) -> Path:
    env_value = os.environ.get("DEEPCODE_WORKSPACE")
    if env_value:
        return Path(env_value).expanduser().resolve()
    if yaml_root:
        return Path(yaml_root).expanduser().resolve()
    return (Path.cwd() / "deepcode_lab").resolve()
