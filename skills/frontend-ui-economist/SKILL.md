---
name: frontend-ui-economist
description: "Editorial and airy plain HTML/CSS/SVG pages: Broadsheet/Airy palettes, inline SVG charts, white-space audits."
---

# Frontend UI themes

- **One token contract, six themes:** Pick Broadsheet, Ink, Briefing (editorial) or Airy, Instrument, Graphite (tool) by subject and job; inspect all six live in `assets/theme-simulator.html`.
- **Tokens only, zero hard-coded hex/px:** Copy `assets/theme-kit.css` (or `references/token-blocks.md`) and space via `--ws` / `--u: calc(8px * var(--ws))`; always give zero lengths units (`0px`, never `0`).
- **Charts & white space:** Use `assets/charts.js` (editorial vs tool style); aim for 55–68% white space measured with `scripts/measure_whitespace.js`; run `node scripts/check_theme.js` for AA contrast + CVD checks.
- **References:** Load `references/themes.md`, `references/token-contract.md`, `references/charts.md`, `references/white-space.md`, `references/anti-patterns.md`, and `references/checklist.md` as needed.

## Workflow

1. **Pick a theme.** Follow any named look in the brief; otherwise pick by subject + job from the table below and state why in one sentence.
2. **Install tokens.** Copy `assets/theme-kit.css` (all six themes + component CSS) or one standalone block from `references/token-blocks.md`. Load only the theme's Google Fonts with a fallback stack; set `--ws`.
3. **Build from tokens only.** Every colour, radius, border, shadow, and spacing value uses `var(--...)` or `calc(var(--u) * N)` — hard-coded hex or `12px` padding breaks theme swaps.
4. **Match chart style to theme group.** Editorial themes use the editorial chart style; tool themes use the tool style via `assets/charts.js` (`references/charts.md`).
5. **Tune white space.** Adjust `--ws` and measure with `node scripts/measure_whitespace.js page.html --selector .main --overlay ink-overlay.html` (`references/white-space.md`), targeting ~55–68% empty content column.
6. **Verify before ship.** Run `node scripts/check_theme.js <css>` (contrast + CVD separation), compare a screenshot against `assets/theme-simulator.html`, walk `references/anti-patterns.md` and `references/checklist.md` (responsive to ~400px, `:focus-visible`, `prefers-reduced-motion`, no clipping).
7. **Hand over** with the theme name, token block/CSS file, and the single intentional visual emphasis.

## Pick a theme

| Theme | Group | Choose it when | Mood in one line |
|---|---|---|---|
| **Broadsheet** | Editorial | Prose-first briefs, research desks, printable reports | White paper, 2px ink top rules, one red per view, serif display |
| **Ink** | Editorial | Low-light Broadsheet sibling; runtime dark swap | Blue-black paper, warm off-white ink, brightened red |
| **Briefing** | Editorial | Chart-led data briefings and analyst dashboards | Cool grey canvas, white panels with blue top bar, navy sidebar, red data highlight |
| **Airy** | Tool | Consumer tools, settings, onboarding, forms | Google-airy: one family, pills, outlined 12px cards, white page, ~60% white space |
| **Instrument** | Tool | Operator consoles, admin, dense developer screens | 13px base, tabular numerals, 6px radius, hairline panels, density as a feature |
| **Graphite** | Tool | Dark-mode-first developer/monitoring tools | Blue-grey neutrals (never pure black), periwinkle accent, 8px radius, borders as elevation |

Read subject -> group (finance/policy/newsroom -> editorial; settings/ops/dev/consumer -> tool), then job -> theme (read -> Broadsheet/Ink, scan charts -> Briefing, dense rows -> Instrument, newcomer -> Airy, night use -> Graphite). Full specs: `references/themes.md`.

## Token contract and components

Every theme sets the ~60 tokens below; components read only tokens (`references/token-contract.md`, `assets/theme-kit.css`):

| Group | Core tokens | Rule |
|---|---|---|
| Colour | `--bg --surface --surface-2 --ink --ink-2 --ink-3 --line --line-2` | Page/panel/alt surfaces, 3 text steps, border/hairline |
| Accent | `--accent --accent-ink --accent-soft --accent-text` | Single brand colour; dark themes brighten `--accent-text` |
| Semantic | `--good --warn --bad` + `*-bg`, `--neutral-bg` | State pills/alerts; separate from accent and chart series |
| Series | `--s1 --s2 --s3 --s4` + `--chart-tab` | Fixed CVD-safe order; never cycle |
| Type & Shape | `--font-display/text/ui`, `--fs-*`, `--radius*`, `--btn-radius`, `--input-radius` | Numbers always use `--font-ui` (never serif); `tabular-nums` in tables |
| Panels & Sidebar | `--panel-*`, `--table-*`, `--input-*`, `--side-*`, `--nav-*` | One elevation mechanism per panel (top rule OR 1px border, never stacked); nav bleeds via `--nav-ml` |
| Space | `--ws` -> `--u: calc(8px * var(--ws))` | Always give lengths units (`0px`, never unitless `0` in `calc()`/`max()`) |

Key component rules (`assets/theme-kit.css`): buttons use accent/outline/ghost/`--bad-btn`; pills pair soft fill + shape (never colour alone); alerts use 3px left semantic border + `--bg` glyph; toasts invert (`--ink`/`--bg`); dialogs use `--dialog-shadow` (1px ink outline in editorial themes); `.article` uses `text-wrap: balance`, 65ch prose, and drop cap only in Broadsheet/Ink.

## Charts, white space, and quality floor

- **Charts (`assets/charts.js`, `references/charts.md`):** Shared rules — 2px lines, 8px end markers with 2px surface ring, hairline gridlines, legend for 2+ series, text in ink tokens (never series colours), single axis, bars <= 24px with 4px rounded data end, hover crosshair + tooltip. **Editorial** puts `--chart-tab` above title, right-aligned y labels on gridlines, inline series names, and a `Sources:` footer. **Tool** puts left y labels, top swatch legend, and line-end values.
- **White space (`references/white-space.md`):** Tune via `--ws` (Airy `1.4`, Instrument `0.8`, others `1`), not by deleting content; verify with `scripts/measure_whitespace.js`.
- **Quality floor (`references/checklist.md`):** Responsive to ~400px, `:focus-visible` accent outline, `prefers-reduced-motion`, body text >= 4.5:1, muted/chart marks >= 3:1, `color-scheme` set; validate contrast + CVD with `scripts/check_theme.js`.

## Avoid generic defaults

Use only when explicitly requested (`references/anti-patterns.md`):
- Warm cream + serif + terracotta, or near-black (`#0b0b0b`/`#111`) + acid-green/vermilion.
- Uniform SaaS card kits with identical rounded soft shadows.
- Template chrome: ALL-CAPS tracked eyebrows, `"A · B · C"` meta rows, arrow on every button, monospace tiny labels, single italic/accented headline word, decorative gradients/scroll animations, fake `01/02/03` numbering.
- Serif numerals in data/KPIs; shadows or multiple red elements in editorial views; pure black in dark themes.

## Boundary with `frontend-ui-modern`

Use this skill for plain HTML/CSS/SVG, server-rendered templates, and editorial/airy static UI. Hand off to `frontend-ui-modern` for shadcn/Tailwind/React (`components.json`) or dotfile themes. Need to port an Economist theme onto shadcn? See `references/shadcn-mapping.md`.

## Files in this skill

| Path | Purpose | Read when |
|---|---|---|
| `references/themes.md` | Six themes in depth (tokens, signature moves, avoids) | after picking a theme |
| `references/token-contract.md` | Full token roles and CSS component recipes | extending a theme or building custom components |
| `references/token-blocks.md` | Standalone CSS token blocks for all six themes + font URL | copying a single theme without `theme-kit.css` |
| `references/charts.md` | Editorial & tool chart rules + validated series palettes | adding any chart |
| `references/white-space.md` | 60% heuristic, ink counting rules, worked measurements | page feels dense or empty |
| `references/anti-patterns.md` | Generic-default tells and self-critique prompts | before shipping |
| `references/checklist.md` | Consolidated pre-ship verification checklist | before shipping |
| `references/shadcn-mapping.md` | Skill boundary + Economist-to-shadcn token port table | porting tokens to shadcn |
| `references/further-reading.md` | Sources (Economist chart guide, Material, WCAG, CVD model) | checking provenance |
| `assets/theme-kit.css` · `assets/charts.js` · `assets/starter.html` · `assets/theme-simulator.html` | Kit CSS, SVG chart helper, minimal starter, live 6-theme simulator | Steps 1–4 |
| `scripts/check_theme.js` · `scripts/measure_whitespace.js` | Contrast/CVD checker (no deps) and Playwright white-space tool | Steps 5–6 |
