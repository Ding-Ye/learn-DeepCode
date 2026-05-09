---
title: "s08 · 循环探测器 + 停滞 vs LLM 偏移"
chapter: 08
slug: s08-loop-detector
est_read_min: 11
---

# s08 · 循环探测器 + 停滞 vs LLM 偏移

> 一个 `LoopDetector` 抓住三种失控（重复、超时、停滞），一个 `ProgressTracker` 记账已完成文件，再加一个 `NoteLLMWait` 偏移让真停滞和 60 秒慢 LLM 区分开。整套机制 ~250 行 Go，**测试用 FakeClock 在微秒内跑完**——纯逻辑，零网络。

---

## Problem

`workflows/code_implementation_workflow.py` 一次跑可能要 30 分钟、30 个文件、每个文件 5-10 轮 tool call。这种长循环里有三种典型卡死：

1. **同一个工具被无限重复** — LLM 卡在某个状态、不停 call `read_file`、永远不进 `write_file`。
2. **总壁钟时间超预算** — 单个文件超过 10 分钟还没写完，几乎肯定是 LLM 进了死结，再等下去也没用。
3. **进度停滞** — 工具调用之间间隔太久，明显有谁阻塞了。

朴素的"停滞探测"——`time.time() - last_progress_time > 300s` 就杀——会**误杀慢 LLM 调用**：长 context、网络抖动、provider 限流，单次模型回包 60-120 秒并不稀奇。把这种**模型侧的等待**计入"停滞"，会在生产环境里随机干掉跑得好好的 pipeline。

上游的修复见 `utils/loop_detector.py:55-58` 注释：

> Wall-clock budget that *excludes* LLM-call time. `note_llm_wait` adds the elapsed LLM seconds back to `last_progress_time` so the stall check only penalises true tool-side inactivity.

s08 的全部任务就是把这个偏移机制原汁原味搬到 Go，并且用 **可注入 Clock** 让单测在微秒级别覆盖所有时间相关的分支。

## Solution

```ascii-anim frames=2
┌──────────────────────────────────────────────────────────────────────┐
│  ┌──────────────┐   CheckTool(name)    ┌────────────────────────┐    │
│  │   Caller     │ ───────────────────▶ │    LoopDetector        │    │
│  │ (s10 file    │                      │  history[]             │    │
│  │  loop)       │ ◀─── Status{...} ─── │  lastProgressAt        │    │
│  └──────────────┘                      │  consecutiveErrors     │    │
│         │                              │  pendingLLMOffset ◀──┐ │    │
│         │ NoteLLMWait(d)               │  startedAt           │ │    │
│         │  (after each LLM call) ────────────────────────────┘ │    │
│         ▼                              └────────────────────────┘    │
│   inside CheckTool:                                                  │
│     1. history.append(name); trim to last 10                         │
│     2. lastProgressAt += pendingLLMOffset; pendingLLMOffset = 0      │
│     3. if last MaxRepeats names are identical → "loop_detected"      │
│     4. if now - startedAt > Timeout         → "timeout"              │
│     5. if now - lastProgressAt > StallThreshold → "stall"            │
│     6. if consecutiveErrors >= MaxErrors    → "max_errors"           │
│     7. else                                 → "ok"                   │
└──────────────────────────────────────────────────────────────────────┘
```

四个 status code（`loop_detected` / `timeout` / `stall` / `max_errors`）跟上游字面一致，方便和 Python 日志对照。`ok` 是 happy path。

核心结构（节选自 [`agents/s08-loop-detector/detector.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s08-loop-detector/detector.go)）：

```go
type LoopDetector struct {
    MaxRepeats     int
    Timeout        time.Duration
    StallThreshold time.Duration
    MaxErrors      int

    clock             Clock
    history           []string
    lastProgressAt    time.Time
    consecutiveErrors int
    pendingLLMOffset  time.Duration
    startedAt         time.Time
}

func (d *LoopDetector) NoteLLMWait(d2 time.Duration) {
    if d2 <= 0 {
        return
    }
    d.pendingLLMOffset += d2
}

func (d *LoopDetector) CheckTool(name string) Status {
    now := d.clock.Now()
    d.history = append(d.history, name)
    if len(d.history) > historyWindow {
        d.history = d.history[len(d.history)-historyWindow:]
    }
    if d.pendingLLMOffset > 0 {
        d.lastProgressAt = d.lastProgressAt.Add(d.pendingLLMOffset)
        d.pendingLLMOffset = 0
    }
    // ... 四个 if 分支按 loop / timeout / stall / max_errors 顺序判定
}
```

## How It Works

**4 个非显然点**：

1. **偏移在 `CheckTool` 里"消费"，不在 `NoteLLMWait` 里立即应用** — 上游 Python 是 `last_progress_time += elapsed_seconds` 立即改；我们故意推迟到下一次 `CheckTool` 里再叠加。语义等价（实际唯一的调用模式都是 note → check），但 Go 这种 staged 设计让 `pendingLLMOffset` 字段在测试里**可观测**：你可以在 note 之后、check 之前断言它不为零，证明 wiring 正确。
2. **`Clock` 是接口，不是 `now func() time.Time`** — 接口让 `*FakeClock` 自带状态（`now time.Time` 字段 + `Advance(d)` 方法），调用点 `WithClock(fc)` 也比 `WithNow(fc.Now)` 易读。代价：多一个声明。收益：测试里 `fc.Advance(31*time.Second)` 然后断言"stall fired"，本地一行代码搞定，不用 `time.Sleep`。
3. **四个判定的次序很重要** — `loop_detected` 要排在 `timeout` 前面，因为一个 1 秒内连调 5 次同名工具的 case 应该报"循环"而不是等到 600 秒后报"超时"。我们的 `if` 链严格按 `loop → timeout → stall → max_errors` 走，第一个命中就 `return`，跟上游一致。
4. **`MaxRepeats < 2` 等价于禁用循环检测** — 任何长度为 1 的"序列"都不可能"重复"。上游也是 `len(self.tool_history) >= self.max_repeats` 隐式禁用，我们显式地写 `if d.MaxRepeats >= 2 && ...` 让意图更清楚。

`ProgressTracker` 是另一回事：它**不做决定**，只记账。`CompleteFile(path)` 去重（path 标准化用上游同款 `replace("\\","/")`+ trim 规则）；`Snapshot()` 返回值类型 `ProgressSnapshot`——`Files` 是一份拷贝，调用方 mutate 它不影响内部状态。

## What Changed (vs. s07)

```diff
+ clock.go     新引入 Clock 接口 + realClock + *FakeClock，注入式时钟范式
+ detector.go  LoopDetector + 4 个 Status code + NoteLLMWait 偏移机制
+ progress.go  ProgressTracker + ProgressSnapshot，互斥锁保护
+ 测试用 FakeClock 把 31 秒等待压到微秒内
- 零 LLM 依赖、零 I/O ——纯逻辑，跟 s07 的 atomic write/jsonl 完全正交
+ 用函数式 Option 暴露阈值（WithMaxRepeats / WithTimeout / ...），治 anti-pattern #10
```

s07 关心的是**断点续跑**（atomic write + jsonl + meta + 5-section validate），s08 关心的是**何时该停下**（loop / timeout / stall / errors）。两者都在 s10 一起被组合：s10 的 file loop 用 s07 的 jsonl 写 attempt 日志，用 s08 的 detector 决定要不要 abort，用 s07 的 IsExistingPlanUsable 决定要不要从 checkpoint resume。

更深的对比：
- s07 是**有状态有 I/O**（写 jsonl、写 checkpoint、磁盘 atomic 操作）；s08 是**有状态零 I/O**（只读时钟、只读自己内存里的几个字段）。两者都是"safety belt"层，但风险面完全不同。
- s07 引入了"`taskDir` 是 s05 给的，我不构造它"的依赖契约；s08 引入了"`Clock` 是构造时注入的，我不直接调 `time.Now()`"的更纯洁的契约。s08 的契约更严，是因为它的全部行为都是时间函数——把时间外置就把测试外置。
- s07 的 `ValidatePlanText` 返回 `[]string` 缺失 section 列表；s08 的 `CheckTool` 返回 `Status{Code, Message, ShouldStop}` 单一结果。两种风格都比上游 Python 的 dict 更类型化。

## Try It

```bash
cd agents/s08-loop-detector

# 跑 demo（5 次同名 tool call → 第 5 次触发 loop_detected）
go run .

# 跑测试（5 PASS，全部用 FakeClock，秒级以下）
go test -v ./...

# vet + build
go vet ./...
go build ./...
```

期望 stdout（demo）：

```
LoopDetector demo: calling "execute_python" five times in a row
---
call #1  code=ok             should_stop=false  message=processing normally
call #2  code=ok             should_stop=false  message=processing normally
call #3  code=ok             should_stop=false  message=processing normally
call #4  code=ok             should_stop=false  message=processing normally
call #5  code=loop_detected  should_stop=true   message=loop detected: "execute_python" called 5 times consecutively
---
aborting on call #5 due to loop_detected
```

测试矩阵（5 个）：

| # | 场景 | 期望 Code |
|---|---|---|
| 1 | 连续 5 次同名 tool | `loop_detected` |
| 2 | 推进时钟超过 Timeout | `timeout` |
| 3 | 推进时钟超过 StallThreshold，**未** NoteLLMWait | `stall` |
| 4 | 推进时钟超过 StallThreshold，**已** NoteLLMWait(stall+1s) | `ok` ← 偏移生效 |
| 5 | 连续 3 次 RecordError 后再 CheckTool | `max_errors` |

第 4 个测试是这一章的"招牌测试"——它单独验证了 `NoteLLMWait` 的偏移机制确实抵消了同等时长的壁钟前进。

## Upstream Source Reading

```upstream:utils/loop_detector.py#L23-L141
class LoopDetector:
    def __init__(
        self,
        max_repeats: int = 5,
        timeout_seconds: int = 600,
        stall_threshold: int = 300,
        max_errors: int = 10,
    ):
        self.max_repeats = max_repeats
        self.timeout_seconds = timeout_seconds
        self.stall_threshold = stall_threshold
        self.max_errors = max_errors

        self.tool_history: List[str] = []
        self.start_time = time.time()
        self.last_progress_time = time.time()
        self.consecutive_errors = 0
        # Wall-clock budget that *excludes* LLM-call time. ``note_llm_wait``
        # adds the elapsed LLM seconds back to ``last_progress_time`` so the
        # stall check only penalises true tool-side inactivity.
        self._pending_llm_offset_s: float = 0.0

    def note_llm_wait(self, elapsed_seconds: float) -> None:
        if elapsed_seconds <= 0:
            return
        self.last_progress_time += elapsed_seconds
```

**阅读笔记**：

- **`note_llm_wait` 的实现只有 2 行** — 上游把所有的复杂度都放在"何时调用"这件事上，调用方（`code_implementation_workflow.py`）每次 `await provider.chat(...)` 后就 `loop_detector.note_llm_wait(time.time() - llm_start)`。我们 Go 港口的等价契约：**任何 LLM 调用之后都必须 `NoteLLMWait(time.Since(start))`**——这条规矩 s10 会贯彻。
- **`set(recent_tools)` 比 Go 的 inner loop 更 Pythonic 但效率没差** — `len(set(...)) == 1` 在 5 个元素上不会比一个 5 次的 for 慢，但更难读懂"我在干嘛"。Go 版的显式 loop 让 reviewer 一眼就能看出"全部相等"的判定。
- **`current_file` / `file_start_time` 在我们的 Go 港口里被合并到 `startedAt`** — 上游的设计假设一个 detector 跨多个文件复用；我们假设 s10 给每个文件 new 一个新的 detector。这个差异让 Go 实现简化了 ~30 行（不需要 `start_file()` / `complete_file()` 的两段式 API），代价是 s10 多一行 `d := NewLoopDetector(...)`。
- **`should_abort()` 和 `get_abort_reason()` 是上游为了支持"事后查询"加的辅助方法** — 它们用 `check_tool_call("")`（传空字符串）来"窥视"状态。我们的 Go 港口删掉了这两个方法：`CheckTool` 返回的 `Status` 已经同时给了"该不该停"和"为什么停"，没必要再开第二个 API。
- **`record_progress` 在上游同时清零 errors 和 offset、并更新时间戳** — 三件事捆在一个方法里。我们的 Go 港口把它拆成 `RecordSuccess`（高层成功）和"`NoteLLMWait` 在下次 check 时被消费"两条独立路径，让"什么算进度"和"什么算 LLM 等待"在类型上分得更开。

**继续读**：从 `loop_detector.py` 进 `workflows/code_implementation_workflow.py:300-450` 看 detector 是如何在每个文件的内层 loop 里被调用、`note_llm_wait` 是何时插入的——那是 s10 要重新组织的代码。再回看 `utils/loop_detector.py:182-253` 的 `ProgressTracker`，注意上游有 phase-percent + ETA 估算（我们故意没移植），那部分逻辑应当属于 s10 的 `RunReport` 层。注解版：[`upstream-readings/s08-loop-detector.py`](../../upstream-readings/s08-loop-detector.py)。

---

**下一章**：s09 把 s06 runner 跑出来的对话历史拿过来做"clean-slate"压缩——每写完一个文件就把对话清空到只剩 system prompt + initial plan + 当前 round 白名单工具结果。这是从"防卡死"到"防 OOM"的另一条防线。
