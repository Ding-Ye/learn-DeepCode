# =============================================================================
#  Annotated extract from HKUDS/DeepCode @ b9ece6035ea3f3582e6c503c517206b23c09ad09
#  File: workflows/code_implementation_workflow.py  (selected, abridged ~150 lines)
#  License: MIT (Copyright 2025 Data Intelligence Lab @ HKU)
# =============================================================================
"""
Paper Code Implementation Workflow - file-by-file iterative development.

This is the architectural climax of upstream — a single class that composes
the runner, the loop detector, the memory agent, and the planning runtime
into one orchestrator. The Go port lives in agents/s10-code-impl-workflow/.
"""

# >>> s10: All four collaborators are imported here. In the Go port s10
#     redeclares the minimal subset of each (no cross-session imports per
#     project rule). The teaching cut also drops MCP, retry, and document
#     segmentation, focusing on the per-file orchestration body.
from utils.loop_detector import LoopDetector, ProgressTracker
from workflows.agents.memory_agent_concise import ConciseMemoryAgent
from workflows.implementation_llm_runtime import call_provider_with_legacy_tools


# >>> s10: Maps to Go's `Workflow` struct in workflow.go. Upstream stores
#     more state (`mcp_agent`, `enable_read_tools`, `_last_run_state`); the
#     Go port keeps only Provider + the per-call configuration knobs and
#     returns RunReport by value rather than mutating self.
class CodeImplementationWorkflow:

    def __init__(self) -> None:
        self.default_models = get_default_models()
        self.logger = self._create_logger()
        self.mcp_agent = None
        self.enable_read_tools = True
        # >>> s10: NewLoopDetector() / NewRunner(p, detector) in the Go
        #     port. Construction order is the same: detector before the
        #     runner so the runner can borrow the detector reference.
        self.loop_detector = LoopDetector()
        self.progress_tracker = ProgressTracker()
        self._last_run_state: Dict[str, Any] = {
            "status": "unknown",
            "reason": None,
            "iterations": 0,
            "elapsed_seconds": 0.0,
            "files_completed": 0,
            "total_files": 0,
            "unimplemented_files": [],
        }

    # >>> s10: Maps to Workflow.Run in workflow.go. The Go port's RunReport
    #     struct mirrors _last_run_state field-for-field. Upstream returns
    #     dict[str, Any]; the Go port returns a typed value.
    async def run_workflow(self, plan_file_path, target_directory=None,
                           pure_code_mode=False, enable_read_tools=True,
                           progress_callback=None):
        plan_content = self._read_plan_file(plan_file_path)
        if target_directory is None:
            target_directory = str(Path(plan_file_path).parent)
        code_directory = os.path.join(target_directory, "generate_code")
        # >>> s10: filepath.Join in the Go port. Same intent: the workflow
        #     never mutates the caller's plan path or task dir.

        # >>> s10: Skipped in the Go port — file-tree creation is tool-driven
        #     (write_file does the mkdir for each file). Upstream does it up
        #     front to give the LLM a scaffold; we let the agent create
        #     directories as it goes.
        if self._check_file_tree_exists(target_directory):
            results["file_tree"] = "Already exists, skipped creation"
        else:
            results["file_tree"] = await self.create_file_structure(
                plan_content, target_directory)

        # >>> s10: implement_code_pure -> _pure_code_implementation_loop
        #     is the body the Go port distills into Workflow.Run's main
        #     `for _, file := range plan.Files` loop.
        if pure_code_mode:
            results["code_implementation"] = await self.implement_code_pure(
                plan_content, target_directory, code_directory,
                progress_callback=progress_callback)

        # >>> s10: The Go port returns RunReport directly with the same
        #     status-taxonomy values: completed | aborted | max_iterations
        #     | max_time | error.
        return {"status": top_status, "inner_status": inner_status,
                "abort_reason": run_state.get("reason"),
                "files_completed": run_state.get("files_completed", 0),
                "total_files": run_state.get("total_files", 0),
                "unimplemented_files": run_state.get("unimplemented_files", []),
                "iterations": run_state.get("iterations", 0),
                "elapsed_seconds": run_state.get("elapsed_seconds", 0.0)}

    # >>> s10: This method is the heart of upstream and the heart of s10.
    #     The Go port's per-file body lives in workflow.go's
    #     implementOneFile; the outer iteration-and-budget logic is in
    #     Workflow.Run.
    async def _pure_code_implementation_loop(self, client, client_type,
                                             system_message, messages, tools,
                                             plan_content, target_directory,
                                             progress_callback=None):
        max_iterations = 800
        iteration = 0
        start_time = time.time()
        max_time = 7200  # 120 minutes
        # >>> s10: run_state ↔ RunReport (typed in Go).
        run_state: Dict[str, Any] = {
            "status": "max_iterations",
            "reason": f"reached max_iterations={max_iterations} without completion",
        }

        memory_agent = ConciseMemoryAgent(plan_content, self.logger,
                                          target_directory, self.default_models,
                                          code_directory)
        # >>> s10: memAgent in the Go port. Same constructor inputs minus
        #     the LLM client (Go's Compact is pure — no LLM round-trip).

        memory_agent.start_new_round(iteration=0)
        while iteration < max_iterations:
            iteration += 1
            elapsed_time = time.time() - start_time
            if elapsed_time > max_time:
                run_state = {"status": "max_time", "reason": ...}
                break
            # >>> s10: The Go port's outer loop checks MaxTime once per
            #     file rather than per inner iteration — finer granularity
            #     would require threading the budget into the runner.

            if self.loop_detector.should_abort():
                run_state = {"status": "aborted", "reason": ...}
                break
            # >>> s10: The Go runner's CheckTool call inside its tool-dispatch
            #     branch covers the same ground — every tool call is gated
            #     by the detector before dispatch.

            llm_start = time.time()
            try:
                response = await self._call_llm_with_tools(...)
            except Exception as e:
                self.loop_detector.note_llm_wait(time.time() - llm_start)
                run_state = {"status": "incomplete", "reason": ...}
                break
            self.loop_detector.note_llm_wait(time.time() - llm_start)
            # >>> s10: NoteLLMWait is in s08 but the s10 detector drops it
            #     for simplicity — replay providers don't take real time.

            if response.get("tool_calls"):
                aborted_in_tool_check = False
                for tool_call in response["tool_calls"]:
                    loop_status = self.loop_detector.check_tool_call(tool_call["name"])
                    if loop_status["should_stop"]:
                        run_state = {"status": "aborted", "reason": ...}
                        aborted_in_tool_check = True
                        break
                if aborted_in_tool_check:
                    break
                # >>> s10: This loop is exactly what the Go runner does in
                #     its pre-tool gate (see runner.go: detector.CheckTool
                #     for each call before dispatching).

                tool_results = await code_agent.execute_tool_calls(
                    response["tool_calls"])

                for tool_call, tool_result in zip(response["tool_calls"], tool_results):
                    is_error = tool_result.get("isError", False)
                    if not is_error:
                        self.loop_detector.record_success()
                        if tool_call["name"] == "write_file":
                            filename = tool_call["input"].get("file_path", "unknown")
                            completed_first_time = self.progress_tracker.complete_file(
                                memory_agent.normalize_file_path(filename))
                            if completed_first_time:
                                print(f"✅ File completed: {filename}")
                    else:
                        self.loop_detector.record_error(...)
                    memory_agent.record_tool_result(...)
                # >>> s10: The Go port's RunSpec.OnToolResult callback
                #     receives every (name, args, result, isError) and
                #     decides what to do. That's where Workflow.Run's
                #     "if name == write_file: memAgent.Compact(messages)"
                #     side-effect lives.

                if memory_agent.should_trigger_memory_optimization(
                        messages, code_agent.get_files_implemented_count()):
                    messages = memory_agent.apply_memory_optimization(
                        current_system_message, messages, files_implemented_count)
                # >>> s10: The Go port doesn't gate Compact behind a
                #     "should optimize" check — it compacts on every
                #     successful write_file. The teaching cut prefers
                #     "always compact" (cheaper to reason about) over
                #     "compact when budget is high" (lighter on tokens).

            # ... emergency trim, completion check ...

            unimplemented_files = memory_agent.get_unimplemented_files()
            if not unimplemented_files:
                run_state = {"status": "completed", "reason": "all planned files implemented"}
                break
        # >>> s10: The Go workflow checks completion via os.Stat per file
        #     rather than a memory_agent flag. Simpler invariant: "the
        #     file exists on disk" is the source of truth.

        elapsed_total = time.time() - start_time
        self._last_run_state = {
            "status": run_state["status"],
            "reason": run_state["reason"],
            "iterations": iteration,
            "elapsed_seconds": elapsed_total,
            "files_completed": len(memory_agent.get_implemented_files()),
            "total_files": len(memory_agent.get_all_files_list()),
            "unimplemented_files": list(memory_agent.get_unimplemented_files() or []),
        }
        # >>> s10: The Go port emits the same shape as RunReport and also
        #     atomic-writes it to taskDir/implementation_report.json so a
        #     debugger can grep the final outcome.
        return await self._generate_pure_code_final_report_with_concise_agents(
            iteration, elapsed_total, code_agent, memory_agent)
