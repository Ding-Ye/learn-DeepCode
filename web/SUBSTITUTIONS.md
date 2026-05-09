# Web shell — substitution guide for Bootstrap (Phase D)

The web shell is a near-verbatim copy of `learn-hermes-agent/web/`. The bootstrap
phase copies this directory and patches a few files to match the target repo.

## What to substitute

### `package.json.tmpl` → `package.json`
- `"name": "learn-hermes-agent-web"` → `"name": "learn-<repo>-web"`

### `lib/curriculum.ts`
- The `CURRICULUM` array is currently filled with hermes's 13 chapters. **Replace** with the planned chapters from `<workdir>/.learn/plan.md`.
- Initially set ALL chapters to `available: false` EXCEPT s01.
- After each session lands (Phase E), the session's subagent will flip its row to `available: true`.

Initial template after Bootstrap:
```typescript
export const CURRICULUM: ChapterMeta[] = [
  // M (multi-model) added in Phase G if has_llm_call_layer
  {
    slug: "s01-<slug>",
    num: "s01",
    title: { zh: "<from plan.md>", en: "<from plan.md>" },
    available: true,
  },
  // s02..sN stubs
  {
    slug: "s02-<slug>",
    num: "s02",
    title: { zh: "<from plan.md>", en: "<from plan.md>" },
    available: false,
  },
  // ... continue with all sessions, s_full, appendices
];
```

### `app/[locale]/page.tsx`
- The "intro" prose (`INTRO_ZH` / `INTRO_EN`) has hermes-specific language. Replace with target-repo-specific version (use research notes).
- Other content: keep as-is.

### `app/[locale]/s/[slug]/page.tsx`
- The `guessUpstreamFile` mapping at the bottom of the file currently has hermes's slugs. Replace with the target's. Initially only `s01-<slug>` mapped; subsequent sessions add their entries during Phase E.

### `app/[locale]/layout.tsx`
- Sidebar branding text. Replace "learn-hermes-agent" with `learn-<repo>`.

### `app/layout.tsx`
- Metadata (title, description). Replace.

### `README.md.tmpl` → `README.md`
- Replace "learn-hermes-agent" → `learn-<repo>`.
- Replace links if needed.

## What NOT to substitute

- Tailwind config / CSS variables / typography — keep verbatim
- Components in `components/` — keep verbatim (LangSwitch, SessionNav, UpstreamReader work generically)
- `lib/content.ts` — keep verbatim (parses ../docs/zh|en/*.md generically)
- TypeScript / Next.js / PostCSS configs — keep verbatim

## Verification

After substitution and `npm install`:
```bash
cd <REPO_ROOT>/web
npm run typecheck   # must pass
npm run build       # must produce static output for s01 in zh + en
```

If build fails because of curriculum.ts type mismatch, the substitution went wrong; re-derive from plan.md.
