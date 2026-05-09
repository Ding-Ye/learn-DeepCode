---
title: "s07 · 规划检查点 + JSONL 尝试日志"
chapter: 07
slug: s07-planning-runtime
est_read_min: 11
---

# s07 · 规划检查点 + JSONL 尝试日志

> 三个落盘文件 + 一个 40 行的形状校验。`AtomicWriteJSON` 用 tmp+fsync+rename 让读者要么看到旧文件要么看到新文件，永远不会半截；`AppendJSONL` 用进程内 `sync.Mutex` 串行追加；`ValidatePlanText` 只问一个问题——文本里有没有提到 5 个必备 section 的名字。这就是上游 `planning_runtime.py` 的全部精华。

---

## Problem

规划阶段一次 LLM 调用要 30 秒以上，偶尔会失败。如果不留痕迹：

- 进程崩溃 → 整次 run 没了，重跑就从零开始；
- 调试一个跑歪的 plan → 没有"上一次试了什么"的面包屑；
- 多次重试 → 中间产物不停覆盖，谁也不知道哪一次写到一半进程被 kill 了；

更阴险的是**写到一半**这个场景：如果 `planning_checkpoint.json` 写了一半进程挂了，下次启动读这个 JSON 会直接 panic——一次崩溃污染了所有未来的重启。Python 上游用的是 `tmp.replace(target)`（POSIX rename 是原子的）；Go 必须照搬，但还得加一步 `fsync`，因为 Go 的 `os.Close` 不像 Python 的 `with` 那样在出栈时帮你 `fsync`，硬崩溃就会丢数据。

最后还有一层：怎么知道 LLM 真的回了一份**像样的** plan？老老实实 `yaml.safe_load`？太严了——上游早就发现规划模型经常吐 markdown 加 YAML fence 的杂合物，严解析就会大量误伤。upstream 选择**只检查 5 个 section 名字是否出现**，这就是 `validate_plan_text` 那段松散的 substring 检查。

## Solution

s07 把 `planning_runtime.py` 拆成 5 个 Go 文件，每个文件解决一件事：

1. **`paths.go`** — 给定 `taskDir`，返回三件套的绝对路径。纯函数，不碰文件系统。
2. **`atomic.go`** — `AtomicWriteJSON(ctx, path, v)`：marshal → 写 `.tmp` → `f.Sync()` → `os.Rename`。任何一步失败都把 `.tmp` 删干净，原 target 文件岿然不动。
3. **`jsonl.go`** — `AppendJSONL(ctx, path, v)`：按 path 取一把进程内 `sync.Mutex`，串行 `O_APPEND` 一行 JSON + `\n`。配套泛型函数 `ReadAllJSONL[T any]` 给测试用。
4. **`validate.go`** — `ValidatePlanText(text) []string`：把文本 lower-case 化，检查 5 个 section 名是不是都作为子串出现。返回**缺失**的那部分；空 slice 等于通过。
5. **`runtime.go`** — `PlanningRuntime{}` 把上面四件事拼起来，加一个 `IsExistingPlanUsable(taskDir) bool` 给 s10 做"能不能续跑"的廉价探测。

整个 s07 不接 LLM，不发请求，不需要 `httptest.Server`。所有测试都在 `t.TempDir()` 里跑完。

## How It Works

```ascii-anim frames=3
┌────────────────────────────────────────────────────────────┐
│  rt := &PlanningRuntime{}                                  │
│                                                            │
│  rt.WriteCheckpoint(ctx, taskDir, Checkpoint{...})         │
│         │                                                  │
│         ▼                                                  │
│  AtomicWriteJSON(ctx, "<taskDir>/planning_checkpoint.json")│
│         │                                                  │
│         ├─▶ marshal v                                      │
│         ├─▶ os.OpenFile(path+".tmp", O_CREATE|O_TRUNC)     │
│         ├─▶ f.Write(data)                                  │
│         ├─▶ f.Sync()      ← 强制 fsync；任一步失败...      │
│         ├─▶ f.Close()                                      │
│         └─▶ os.Rename(tmp, path) ← ...都会删 tmp 并保留旧  │
│                                                            │
│  rt.RecordAttempt(ctx, taskDir, Attempt{OK:false, ...})    │
│         │                                                  │
│         ▼                                                  │
│  AppendJSONL(ctx, "<taskDir>/planning_attempts.jsonl")     │
│         ├─▶ lockFor(absPath).Lock()  ← 进程内串行          │
│         ├─▶ os.OpenFile(O_APPEND|O_CREATE)                 │
│         ├─▶ f.Write(json + "\n")                           │
│         └─▶ Unlock                                         │
└────────────────────────────────────────────────────────────┘
```

核心 ~40 行（节选自 [`agents/s07-planning-runtime/atomic.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s07-planning-runtime/atomic.go) + [`runtime.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s07-planning-runtime/runtime.go)）：

```go
func AtomicWriteJSON(ctx context.Context, path string, v any) error {
    data, _ := json.MarshalIndent(v, "", "  ")
    _ = os.MkdirAll(filepath.Dir(path), 0o755)

    tmp := path + ".tmp"
    f, _ := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
    if _, err := f.Write(data); err != nil { _ = f.Close(); _ = os.Remove(tmp); return err }
    if err := f.Sync();          err != nil { _ = f.Close(); _ = os.Remove(tmp); return err }
    if err := f.Close();         err != nil {                _ = os.Remove(tmp); return err }
    if err := os.Rename(tmp, path); err != nil {              _ = os.Remove(tmp); return err }
    return nil
}

func ValidatePlanText(text string) []string {
    lower := strings.ToLower(text)
    var missing []string
    for _, s := range RequiredPlanSections {
        if !strings.Contains(lower, s) { missing = append(missing, s) }
    }
    return missing
}
```

**5 个非显然点**：

1. **`fsync` 是补 Python 没做的功课** — 上游 `tmp.replace(target)` 在内核层面是原子的，但**数据落盘**不是：rename 完了，extents 可能还在 page cache。Linux 上 ext4 的 data=ordered 默认会保护你，但其它文件系统/挂载选项不一定。Go 多加一行 `f.Sync()`，零成本，多一份保险。
2. **`PlanningRuntime` 的零值就能用** — 没有 `New()` 函数，因为这个类型不持有任何 I/O 状态。每个方法都接 `taskDir` 参数自己算路径。这让上层（s10）想为不同任务并发跑两个 PlanningRuntime 就直接 `&PlanningRuntime{}` 两个。
3. **`AppendJSONL` 的锁是按"绝对路径"取的，不是全局** — 全局锁会让两个无关的 task 互相串行；用 `filepath.Abs(path)` 做 key 取一把锁，同 task 内的两个 goroutine 串行（必须的），不同 task 完全并发。
4. **`ReadAllJSONL[T any]` 是泛型，不是 `[]map[string]any`** — Go 1.18+ 泛型让测试可以直接 `ReadAllJSONL[Attempt](path)`，省掉 `json.Unmarshal` 第二趟。生产代码不用这个函数（生产读日志会流式逐行处理），但测试断言变得极度自然。
5. **`ValidatePlanText` 不解析 YAML** — 上游也不强制——它在 substring 检查通过后才"试探性" `yaml.safe_load`。Go 干脆只做 substring，连 YAML 解析都不做。理由有二：(a) 解析失败时 upstream 自己也是回退到 substring 结果；(b) Go 标准库没 YAML，光为了校验就引入第三方依赖太重。

## What Changed (vs. s06 / 之前的章节)

```diff
+ 第一次落盘 —— s01..s06 都是纯内存 + httptest 黑盒测试，s07 是第一个"真正写文件"的章节
+ 引入 atomic write (tmp+fsync+rename) —— s10 还会用同一个原语
+ 引入 JSONL append + 进程内互斥 —— 同样会在 s10 per-file 日志复用
+ 引入"零值即可用"的 Runtime 模式 —— PlanningRuntime{} 就是一个完整对象
+ 引入泛型测试辅助 ReadAllJSONL[T any]
- 零 LLM 依赖、零 Provider 依赖 —— 这是 s05 之后第二个完全脱离对话的章节
```

之前的章节都假设"对话在内存里走完一次就结束了"——s01 的 round-trip、s02 的工具表、s06 的 Runner 循环都没落盘。s07 第一次承认**长流程必须有断点**：30 秒一次的 LLM 调用、几十次重试、几小时的 implementation phase——不存进盘里，一旦中断就什么都没了。

更深一层的对比：之前的章节都在解决"一次会话内"的问题（怎么调模型、怎么回应工具、怎么管上下文）；s07 解决的是"跨进程边界"的问题（崩溃恢复、审计追踪、并发追加）。这是一个完全独立的轴——你可以把 s07 拿掉，agent 还是跑得起来，只是丢了断点续跑能力。

## Try It

```bash
cd agents/s07-planning-runtime

# 校验一份候选 plan（5 个 section 都在）
cat > /tmp/good_plan.md <<'EOF'
file_structure: main.go
implementation_components: parser
validation_approach: go test
environment_setup: go 1.23
implementation_strategy: top-down
EOF
go run . /tmp/good_plan.md

# 校验一份缺 section 的 plan
cat > /tmp/bad_plan.md <<'EOF'
file_structure: main.go
implementation_components: parser
EOF
go run . /tmp/bad_plan.md   # 退出码 3，列出 3 个缺失 section

# 看落盘的 attempt 日志
cat $TMPDIR/learn-deepcode-s07/planning_attempts.jsonl

# 测试
go test -v ./...
```

期望 stdout（good_plan）：

```
OK — all 5 required sections present (158 bytes)
```

期望 stdout（bad_plan）：

```
MISSING 3/5 sections:
  - validation_approach
  - environment_setup
  - implementation_strategy
```

测试：5 PASS，<1s，全部用 `t.TempDir()`。

## Upstream Source Reading

```upstream:workflows/planning_runtime.py#L18-L72
REQUIRED_PLAN_SECTIONS = (
    "file_structure",
    "implementation_components",
    "validation_approach",
    "environment_setup",
    "implementation_strategy",
)


def planning_paths(paper_dir: str | Path) -> dict[str, Path]:
    root = Path(paper_dir)
    return {
        "checkpoint": root / "planning_checkpoint.json",
        "attempts":   root / "planning_attempts.jsonl",
        "meta":       root / "planning_result_meta.json",
    }


def write_json(path: str | Path, payload: dict[str, Any]) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp = target.with_suffix(target.suffix + ".tmp")
    tmp.write_text(
        json.dumps(payload, ensure_ascii=False, indent=2, default=str),
        encoding="utf-8",
    )
    tmp.replace(target)


def append_jsonl(path: str | Path, payload: dict[str, Any]) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(payload, ensure_ascii=False, default=str))
        handle.write("\n")
```

**阅读笔记**：

- **`REQUIRED_PLAN_SECTIONS` 是一个跨模块契约** — prompt 模板里指示 LLM 必须输出这 5 个 key、validator 用同一个 tuple 检查、s10 的代码生成 phase 又会按这个名字读 plan。任何地方改名字都要三处同步——所以两边都把它定义为不可变常量（Python 用 tuple；Go 用 `var ... = []string{...}`，理论上可变但只读约定足够）。
- **`tmp.replace(target)` 的语义** — 这是 `os.replace`，POSIX `rename(2)` 的 Python 包装。它的关键性质是"在同一个目录内是原子的"——所以代码用 `target.with_suffix(suffix + ".tmp")` 在**同一目录**里拿一个 sibling 名字，而不是 `/tmp` 之类的另一个文件系统（rename 跨文件系统会报 `EXDEV` 不是原子）。Go 的 `os.Rename` 同样依赖这一点，atomic.go 的 `dir := filepath.Dir(path)` 就是为此。
- **Python 的 `with target.open("a")` 在 GIL 下是行原子的** — 一个进程内多线程同时 append，写入仍然不会交错（因为 GIL）。Go 没 GIL，所以必须显式 `sync.Mutex`。这是一个上游隐式靠语言运行时、Go 必须显式补的点。
- **没用 `json-repair`** — 上游别处常常用 `json-repair` 库容错解析 LLM 输出的"近似 JSON"，但 `planning_runtime.py` 自己写出去的都是 stdlib `json.dumps`，因为它的对端是确定性的（自己读自己写）。这是个值得学的边界划分：**外部输入**（LLM）容错解析，**内部状态**（自己的检查点）严格 stdlib——一旦容错，bug 会渗到所有未来的快照里。
- **`coerce_text_to_minimal_plan` 是规划失败的兜底** — 这一段 60 多行的"YAML 脚手架"被故意留在 Python 里没 port——它是一个**策略**（"模型不肯按格式走，那我们硬塞一个最小 plan 让 implementation 接得上"），不是一个**机制**。机制（持久化、原子写、追加日志、形状校验）是 s07；策略（出错怎么兜底）属于 s10 的编排逻辑。

**继续读**：从 `planning_runtime.py` 进 `agent_orchestration_engine.py` 的第 4 个 phase，看 `_run_planning(...)` 怎么把 `build_planning_checkpoint_callback` 注入到 `AgentRunSpec.checkpoint_callback`——那是 Go 这一节没翻译的"异步回调注入"模式（Go 直接 `for` 循环里调 `WriteCheckpoint`，没回调）。注解版：[`upstream-readings/s07-planning.py`](../../upstream-readings/s07-planning.py)。

---

**下一章**：s08 把"运行得太久 / 一直调同一个 tool / 错误攒太多"这三类异常合并成一个 `LoopDetector`，并解决一个常被忽略的反模式——把 LLM 网络延迟错算成"卡住"。
