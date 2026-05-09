// The locked curriculum from the plan. SessionNav and the landing page both
// read from this single source of truth. Slugs match docs/{zh,en}/<slug>.md.
//
// "available: false" means the chapter exists in the curriculum but its
// docs aren't written yet — the link will render but go to a placeholder.

export type ChapterMeta = {
  slug: string;
  num: string; // "s01", "s02", "s_full"
  title: { zh: string; en: string };
  available: boolean;
};

export const CURRICULUM: ChapterMeta[] = [
  {
    slug: "multi-model",
    num: "M",
    title: {
      zh: "多模型接入指南（OpenAI / DeepSeek / Qwen / 自托管 …）",
      en: "Multi-model guide (OpenAI / DeepSeek / Qwen / self-hosted …)",
    },
    available: false,
  },
  {
    slug: "s01-minimum-loop",
    num: "s01",
    title: { zh: "最小智能体回路", en: "Minimum agent loop" },
    available: true,
  },
  {
    slug: "s02-tool-registry",
    num: "s02",
    title: { zh: "工具注册表", en: "Tool registry" },
    available: true,
  },
  {
    slug: "s03-config-loader",
    num: "s03",
    title: {
      zh: "单 JSON 配置 + 阶段覆盖",
      en: "Single-JSON config + phase overrides",
    },
    available: true,
  },
  {
    slug: "s04-provider-abstraction",
    num: "s04",
    title: { zh: "LLM Provider 抽象", en: "LLM provider abstraction" },
    available: true,
  },
  {
    slug: "s05-workflow-context",
    num: "s05",
    title: {
      zh: "不可变工作流上下文",
      en: "Immutable workflow context",
    },
    available: true,
  },
  {
    slug: "s06-tool-capable-runner",
    num: "s06",
    title: {
      zh: "可调用工具的 Runner",
      en: "Tool-capable agent runner",
    },
    available: true,
  },
  {
    slug: "s07-planning-runtime",
    num: "s07",
    title: {
      zh: "规划检查点 + JSONL 尝试日志",
      en: "Planning checkpoint + JSONL attempts",
    },
    available: true,
  },
  {
    slug: "s08-loop-detector",
    num: "s08",
    title: {
      zh: "循环探测器 + 停滞 vs LLM 偏移",
      en: "Loop detector + stall vs LLM offset",
    },
    available: true,
  },
  {
    slug: "s09-memory-compaction",
    num: "s09",
    title: {
      zh: "记忆压缩（清空式策略）",
      en: "Memory compaction (clean-slate)",
    },
    available: true,
  },
  {
    slug: "s10-code-impl-workflow",
    num: "s10",
    title: {
      zh: "文件级代码实现工作流",
      en: "File-by-file code implementation workflow",
    },
    available: true,
  },
  {
    slug: "s_full-integration",
    num: "s_full",
    title: { zh: "端到端集成", en: "End-to-end integration" },
    available: false,
  },
  {
    slug: "appendix-a-multi-agent-philosophy",
    num: "A",
    title: {
      zh: "附录 A · 多智能体编排哲学",
      en: "Appendix A · Multi-agent orchestration philosophy",
    },
    available: false,
  },
  {
    slug: "appendix-b-upstream-map",
    num: "B",
    title: {
      zh: "附录 B · 上游源码导读地图",
      en: "Appendix B · Upstream source-reading map",
    },
    available: false,
  },
];

export type Locale = "zh" | "en";

export function chapterTitle(c: ChapterMeta, locale: Locale): string {
  return c.title[locale];
}
