---
title: "s05 · 不可变工作流上下文"
chapter: 05
slug: s05-workflow-context
est_read_min: 10
---

# s05 · 不可变工作流上下文

> 一个 5 字段的 `WorkflowContext` 值类型，由 `Prepare(input, opts)` 构造一次，然后在 11 个 phase 中只读地穿透。Go 没有 `frozen=True`——但**值类型 + 未导出字段 + 只读访问器**给了你同等的、由编译器强制的不可变性。

---

## Problem

把"原始字符串 + dict"在十几个 phase 之间穿来穿去，是上游 DeepCode 历史上最大的 bug 来源。`paper_path` 是相对路径还是绝对路径？`task_id` 是 UUID 还是 8 位 hex？`workspace_root` 这一刻是 `os.getcwd()/deepcode_lab` 还是 `~/.deepcode/`？谁在 Phase 4 改了 `dir_info["reference_path"]`，Phase 7 拿到的就不是 Phase 2 写下去的那个字符串了。

`workflows/workflow_context.py` 的解决方案是把这一切**冻结**进一个 `@dataclass(slots=True)`：所有字段类型化、所有路径都是 `pathlib.Path`、构造一次后就只通过 `@property` 派生新值。Phase 2/3 仍允许填 `paper_path` 等可选字段（这是上游一个未完美贯彻的妥协），但 Phase 4 之后整个对象事实上不可变。

但 Python 的 `@dataclass` 默认**不是**真不可变（要 `frozen=True` 才行；上游也没用）。怎么在 Go 里给到比上游更强的保证？

## Solution

Go 没有 `dataclass(frozen=True)`，但有更强的等价物：

1. **值类型而非指针** — `func phase4(ctx WorkflowContext)` 默认按值传递，每次调用都是一份独立拷贝。即使被调函数试图修改它的副本，调用者手里的原件岿然不动。
2. **未导出字段 + 只读访问器** — `taskID` / `inputSource` 等字段都是小写，包外完全看不见。包外想"修改"必须经过导出方法——而我们故意一个 setter 都不写。这是**编译期**而非运行时强制的不可变。
3. **`Prepare` 是唯一构造入口** — 输入 kind 检测、task ID 分配、workspace 解析全部集中在 `Prepare(input string, opts Options)`。其它代码拿不到字段，就只能读不能写。

副作用还有一个：因为所有字段都是可比较类型（string × 5），`==` 直接能用——`before == after` 就是结构相等比较，不用写 `Equal()` 方法。这是 Python 要 `@dataclass(eq=True)` 才有的能力。

## How It Works

```ascii-anim frames=2
┌──────────────────────────────────────────────────────────┐
│  Prepare("paper.pdf", Options{WorkspaceRoot: "/tmp/x"})  │
│         │                                                │
│         ▼                                                │
│  detectInputKind("paper.pdf") = "pdf"                    │
│  generateTaskID()             = "task_a1b2c3d4"          │
│  resolveWorkspaceRoot(opts)   = "/tmp/x"                 │
│         │                                                │
│         ▼                                                │
│  WorkflowContext{                                        │
│    taskID:        "task_a1b2c3d4",                       │
│    inputSource:   "paper.pdf",                           │
│    inputKind:     "pdf",                                 │
│    workspaceRoot: "/tmp/x",                              │
│    taskDir:       "/tmp/x/tasks/task_a1b2c3d4"           │
│  }                                                       │
│         │                                                │
│         ▼  ctx.ReferencePath()                           │
│  filepath.Join(taskDir, "reference.md")                  │
│  → "/tmp/x/tasks/task_a1b2c3d4/reference.md"             │
└──────────────────────────────────────────────────────────┘
```

核心 ~30 行（节选自 [`agents/s05-workflow-context/context.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s05-workflow-context/context.go) + [`prepare.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s05-workflow-context/prepare.go)）：

```go
type WorkflowContext struct {
    taskID        string
    inputSource   string
    inputKind     InputKind
    workspaceRoot string
    taskDir       string
}

func (c WorkflowContext) TaskID() string        { return c.taskID }
func (c WorkflowContext) InputKind() InputKind  { return c.inputKind }
func (c WorkflowContext) TaskDir() string       { return c.taskDir }
// ... 其余三个访问器同形

func Prepare(input string, opts Options) (WorkflowContext, error) {
    if strings.TrimSpace(input) == "" {
        return WorkflowContext{}, &EmptyInputError{}
    }
    taskID := opts.TaskIDOverride
    if taskID == "" {
        var err error
        taskID, err = generateTaskID()
        if err != nil {
            return WorkflowContext{}, err
        }
    }
    root, err := resolveWorkspaceRoot(opts)
    if err != nil {
        return WorkflowContext{}, err
    }
    return WorkflowContext{
        taskID:        taskID,
        inputSource:   input,
        inputKind:     detectInputKind(input),
        workspaceRoot: root,
        taskDir:       filepath.Join(root, "tasks", taskID),
    }, nil
}
```

派生路径全部走 `filepath.Join`（[`paths.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s05-workflow-context/paths.go)）：

```go
func (c WorkflowContext) ReferencePath() string {
    return filepath.Join(c.taskDir, "reference.md")
}
func (c WorkflowContext) LogsDir() string {
    return filepath.Join(c.taskDir, "logs")
}
// ... 其余三个同形
```

**4 个非显然点**：

1. **`Prepare` 不创建任何目录** — 纯计算函数。任务目录在 s07/s10 才被真正 mkdir。这让 s05 的测试在 `t.TempDir()` 都用不上的情况下毫秒级跑完——纯内存的 `filepath.Join`。
2. **`task_<8 hex>` 用 `crypto/rand`，不是 `math/rand`** — 即便是不安全的 ID 用途（无非是文件夹名字防撞），用 crypto/rand 也是 4 字节、零额外成本。让 `math/rand` 离 task ID 远一点，免得哪天有人 misread 成可猜的 token。
3. **`detectInputKind` 让 URL 优先于扩展名** — `https://x.com/paper.pdf` 是 URL 而不是 PDF。上游同样这么做（PDF 的实际下载是 Phase 2 的事；Phase 1 只关心"要去 fetch 还是直接 read"）。
4. **派生路径用值接收器** — `func (c WorkflowContext) ReferencePath() string`，不是 `(c *WorkflowContext)`。值接收器进一步强化"我不会修改任何东西"的契约——读者一眼能看出这是查询不是变更。

## What Changed (vs. s04)

```diff
+ context.go    引入 WorkflowContext 值类型 + InputKind string 枚举 + 5 个只读访问器
+ prepare.go    单一构造入口 Prepare()；input kind 检测表；*EmptyInputError 类型化错误
+ paths.go      5 个派生路径方法，全部 filepath.Join
- 零 LLM 依赖 —— 这一节是纯数据 + 路径运算
+ 测试用 filepath.ToSlash 做 OS-portable 字符串比对
+ 引入"值类型 + 未导出字段 = Go 的 frozen="这个范式
```

s04 关心的是**协议翻译**（Anthropic vs OpenAI 怎么映射到同一个 ChatResponse）；s05 关心的是**任务身份**（这一次跑的所有 phase 共享什么不变量）。两个层完全独立——s05 不知道 Provider 存在，s04 也不知道任何 task 的存在。

更深的对比：
- s01-s04 的所有数据结构都是**可变**的（`Registry` 的 map 显然要 mutate；`Config` 也允许字段被改）。s05 是第一个**只读**类型——这开了个先例：往后只要某个值"代表一次任务的身份"，就用这个范式。
- s01-s04 还都接受 LLM 依赖（要发请求、要给 schema、要解 finish_reason）。s05 是第一个**纯数据 + filepath** 的章节，证明"不可变上下文"作为一个抽象，跟"对话"完全正交。

## Try It

```bash
cd agents/s05-workflow-context

# 本地路径
go run . paper.pdf

# URL
go run . https://arxiv.org/abs/2401.01234

# 自定义 workspace
go run . -workspace /tmp/learn-ws spec.md

# 测试
go test -v ./...
```

期望 stdout（PDF 输入）：

```
task_id:        task_a1b2c3d4
input_source:   paper.pdf
input_kind:     pdf
workspace_root: /Users/you/.deepcode-learn
task_dir:       /Users/you/.deepcode-learn/tasks/task_a1b2c3d4
---
reference_path:              /Users/you/.deepcode-learn/tasks/task_a1b2c3d4/reference.md
initial_plan_path:           /Users/you/.deepcode-learn/tasks/task_a1b2c3d4/initial_plan.md
implementation_report_path:  /Users/you/.deepcode-learn/tasks/task_a1b2c3d4/implementation_report.md
logs_dir:                    /Users/you/.deepcode-learn/tasks/task_a1b2c3d4/logs
generate_code_dir:           /Users/you/.deepcode-learn/tasks/task_a1b2c3d4/generate_code
```

测试：5 PASS，纳秒级。无文件系统写入，无网络。

## Upstream Source Reading

```upstream:workflows/workflow_context.py#L62-L126
@dataclass(slots=True)
class WorkflowContext:
    """Everything the pipeline needs to know about one task."""

    task_id: str
    input_source: str
    input_kind: InputKind
    workspace_root: Path
    task_dir: Path
    enable_indexing: bool
    task_kind: TaskKind = "paper2code"
    skip_research_analysis: bool = False
    paper_path: Path | None = None
    paper_md_path: Path | None = None
    standardized_text: str | None = None

    @property
    def reference_path(self) -> Path:
        return self.task_dir / "reference.txt"

    @property
    def initial_plan_path(self) -> Path:
        return self.task_dir / "initial_plan.txt"

    @property
    def implementation_report_path(self) -> Path:
        return self.task_dir / "code_implementation_report.txt"
```

**阅读笔记**：

- **`@dataclass(slots=True)` 的妥协** — 上游用 slots 省内存，但**没用** `frozen=True`。原因是 Phase 2/3 仍要回填 `paper_path` 和 `standardized_text`。这是一个"原本想做不可变，但被两个 phase 拖回来"的现实例子。我们的 Go 港口选择更激进：把那些"会变"的字段从 Context 里剥离（如果未来要用，就放进各自 Phase 的 Output 值类型里）——这样 Context 变成真正不可变。
- **`pathlib.Path` 的精髓** — 上游用 `Path` 让 `task_dir / "reference.txt"` 这种表达式合法。Go 没有运算符重载，所以我们用 `filepath.Join(task_dir, "reference.txt")`。两者效果等价，可读性差异可忽略。
- **`to_dir_info()` 是历史包袱** — 上游为了兼容老的 stringly-typed Phase 4-10，还得提供一个"导出成 dict"的方法。我们直接不要这个 API：Go 调用方用访问器方法读字段，比读 dict 还方便。如果未来要 JSON 序列化，加一个 `MarshalJSON` 就够了。
- **`resolve_workspace_root` 的三层优先级** — 上游 env > yaml > cwd。我们简化成两层（opts.WorkspaceRoot > $HOME/.deepcode-learn），把环境变量插值的责任推给 s03 的 config loader。每个章节做一件事这一原则。
- **为什么 `EXTENSION_TO_KIND` 表值得抠细节** — `.markdown` → `md`、`.doc` → `docx`、`.htm` → `html` 这三条都是真实用户会撞到的边角。我们的 Go 表逐条照搬，并写在和扩展名 lookup 同一个文件里——单测就能挡住"哪天有人手抖删了一行"。

**继续读**：从 `workflow_context.py` 进 `workflows/environment.py` 看 `prepare_workflow_environment`——它才是真正做 mkdir + Phase 0 副作用的地方（s10 才会重做这一步）。沿 `task_dir` 的使用进 `workflows/code_implementation_workflow.py` 看 `task_dir / "generate_code" / file_name` 的字符串拼接——那里就是 s10 要重新组织的代码。注解版：[`upstream-readings/s05-workflow-context.py`](../../upstream-readings/s05-workflow-context.py)。

---

**下一章**：s06 把 s02 的 Registry、s04 的 Provider 拼接成可调用工具的 Runner——一个真正的 agent 循环：调模型 → 检测 tool_use → dispatch tool → 喂回 tool_result → 重复直到模型说完。
