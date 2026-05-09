# =============================================================================
#  Annotated extract from HKUDS/DeepCode @ b9ece6035ea3f3582e6c503c517206b23c09ad09
#  File: utils/loop_detector.py  (L1-L180, abridged)
#  License: MIT (Copyright 2025 Data Intelligence Lab @ HKU)
# =============================================================================
"""
Loop Detection and Timeout Safeguards for Code Implementation Workflow

This module provides tools to detect infinite loops, timeouts, and progress stalls
in the code implementation process to prevent hanging processes.
"""

# >>> s08: Python uses time.time() everywhere. Go uses a Clock interface
#     (clock.go) so tests can substitute a *FakeClock. The production
#     realClock{} is a one-liner that delegates to time.Now().
import time
from typing import List, Dict, Any, Optional


# >>> s08: Maps to Go's `LoopDetector` struct in detector.go. Same four
#     thresholds, but we expose them as functional options
#     (WithMaxRepeats / WithTimeout / WithStallThreshold / WithMaxErrors)
#     instead of constructor kwargs. The motivation is research-notes
#     anti-pattern #10: thresholds were hardcoded in upstream's earliest
#     versions. Our Go port makes them obviously-pluggable from the
#     constructor signature alone.
class LoopDetector:
    def __init__(
        self,
        max_repeats: int = 5,
        timeout_seconds: int = 600,
        stall_threshold: int = 300,
        max_errors: int = 10,
    ):
        # >>> s08: Same defaults — 5 / 600s / 300s / 10. Verbatim.
        self.max_repeats = max_repeats
        self.timeout_seconds = timeout_seconds
        self.stall_threshold = stall_threshold
        self.max_errors = max_errors

        # >>> s08: Maps to LoopDetector.history (last 10 tool names).
        self.tool_history: List[str] = []
        # >>> s08: Maps to LoopDetector.startedAt and lastProgressAt. We
        #     seed both from clock.Now() inside NewLoopDetector so a
        #     fresh detector never spuriously trips on a Unix-epoch start.
        self.start_time = time.time()
        self.last_progress_time = time.time()
        self.consecutive_errors = 0
        self.current_file = None
        self.file_start_time = None
        # >>> s08: This is the load-bearing field. Go counterpart:
        #     LoopDetector.pendingLLMOffset (time.Duration). The upstream
        #     comment below is the canonical explanation of the mechanism —
        #     read it before you touch CheckTool's stall arithmetic.
        # Wall-clock budget that *excludes* LLM-call time. ``note_llm_wait``
        # adds the elapsed LLM seconds back to ``last_progress_time`` so the
        # stall check only penalises true tool-side inactivity.
        self._pending_llm_offset_s: float = 0.0

    # >>> s08: Maps to LoopDetector.CheckTool(name) returning a typed
    #     Status{Code, Message, ShouldStop}. Upstream's dict has the same
    #     three keys; we just give them a struct so callers don't typo.
    def check_tool_call(self, tool_name: str) -> Dict[str, Any]:
        current_time = time.time()
        self.tool_history.append(tool_name)

        # >>> s08: Same ring-buffer trim. Go uses a slice + an explicit
        #     `len(d.history) > historyWindow` check.
        if len(self.tool_history) > 10:
            self.tool_history = self.tool_history[-10:]

        # >>> s08: Same loop-detection rule: last max_repeats entries are
        #     all the same tool name. Go's version uses a tiny inner loop
        #     instead of `len(set(...)) == 1` — but the predicate is identical.
        if len(self.tool_history) >= self.max_repeats:
            recent_tools = self.tool_history[-self.max_repeats :]
            if len(set(recent_tools)) == 1:
                return {
                    "status": "loop_detected",
                    "message": f"Loop detected: {tool_name} called {self.max_repeats} times consecutively",
                    "should_stop": True,
                }

        # >>> s08: Upstream uses file_start_time for per-file timeout. Our
        #     Go port folds this into the single startedAt field — s10
        #     constructs a fresh LoopDetector per file, so the budget is
        #     per-file by construction.
        if (
            self.file_start_time
            and (current_time - self.file_start_time) > self.timeout_seconds
        ):
            return {"status": "timeout", "message": "...", "should_stop": True}

        # >>> s08: This is where the offset is consumed. Upstream applies
        #     the offset inside note_llm_wait (it shifts last_progress_time
        #     forward immediately). Our Go port stages the offset in
        #     pendingLLMOffset and applies it inside the next CheckTool.
        #     Behaviourally equivalent; the Go form makes the "consume on
        #     check" semantics explicit.
        if (current_time - self.last_progress_time) > self.stall_threshold:
            return {"status": "stall", "message": "...", "should_stop": True}

        if self.consecutive_errors >= self.max_errors:
            return {"status": "max_errors", "message": "...", "should_stop": True}

        return {"status": "ok", "message": "Processing normally", "should_stop": False}

    # >>> s08: Maps to LoopDetector.RecordSuccess (consecutiveErrors=0,
    #     lastProgressAt=now, pendingLLMOffset=0). Upstream collapses two
    #     responsibilities into one method; we do the same.
    def record_progress(self):
        self.last_progress_time = time.time()
        self.consecutive_errors = 0
        self._pending_llm_offset_s = 0.0

    # >>> s08: Maps to LoopDetector.NoteLLMWait(d). Upstream applies the
    #     offset eagerly (mutates last_progress_time directly); our Go
    #     port stages it in pendingLLMOffset and applies on the next
    #     CheckTool. Both flavours are equivalent for the only realistic
    #     call pattern (note then check). The Go variant is friendlier
    #     to test because pendingLLMOffset is observable mid-test.
    def note_llm_wait(self, elapsed_seconds: float) -> None:
        if elapsed_seconds <= 0:
            return
        self.last_progress_time += elapsed_seconds


# >>> s08: Maps to ProgressTracker in progress.go. We drop the phase-percent
#     + ETA logic — those are presentation concerns that belong in s10's
#     RunReport, not in the safety-belt layer.
class ProgressTracker:
    def __init__(self, total_files: int = 0):
        self.total_files = total_files
        self.completed_files = 0
        # >>> s08: Maps to ProgressTracker.completed (map[string]bool) +
        #     files ([]string). Upstream uses a set for membership and
        #     loses ordering; Go keeps both so the snapshot can return a
        #     stable, ordered list of completed paths.
        self.completed_file_paths = set()

    @staticmethod
    def _normalize_file_path(filename: str) -> str:
        # >>> s08: Same normalization rule (backslash → slash, trim
        #     whitespace + leading/trailing slashes).
        return str(filename or "").replace("\\", "/").strip().strip("/")

    def complete_file(self, filename: str) -> bool:
        # >>> s08: Returns True on first completion, False on duplicates.
        #     Go counterpart returns the same bool with the same semantics.
        normalized = self._normalize_file_path(filename)
        if normalized and normalized in self.completed_file_paths:
            return False
        if normalized:
            self.completed_file_paths.add(normalized)
        self.completed_files += 1
        return True
