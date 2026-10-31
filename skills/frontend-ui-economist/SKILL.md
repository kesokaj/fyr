---
name: frontend-ui-economist
description: "Editorial, report and airy UI in plain HTML/CSS/SVG: six themes on one token contract, an SVG chart kit, a white-space method. Use for static pages, briefs, or a UI that looks generic. Do NOT use for shadcn, Tailwind or React (frontend-ui-modern)."
---

# Frontend UI themes

One token contract, six themes that fill it, and the rules that keep a page from drifting into the generic
AI look. Everything here was built and checked in a browsable simulator (`assets/theme-simulator.html`,
also published at claude.ai) where the same tool app renders in all six themes; the screenshots in
`assets/screenshots/` are what "correct" looks like, so compare your work against them.

Why a contract rather than a pile of CSS: when every theme sets the same ~60 variables, a page written
against the tokens can change its whole personality by swapping one block, and components never need
to know which theme they are in. That is also what makes the themes safe to extend: add a seventh by
filling the same contract and running the same checks.

## Workflow

1. **Read the brief, then pick a theme.** If the brief names a look, follow it exactly, even if it is one
   of the generic defaults below. If it leaves the look open, choose by subject and job using the table in
   "Pick a theme", not by habit. Say which theme you chose and why in one sentence.
2. **Install the tokens.** Copy `assets/theme-kit.css` (all six themes + component CSS) or just the theme's
   block from "Theme token blocks" into the page's `:root`/`[data-theme]`. Load the Google Fonts the theme
   names, with a real fallback stack. Set `--ws` to the theme's default spacing multiplier.
3. **Build from tokens only.** Every colour, radius, border, shadow and spacing value comes from a token
   (`var(--accent)`, `var(--radius)`, `calc(var(--u) * 2)`). A hard-coded hex or `12px` padding is a bug,
   because it will not move when the theme does. Use the recipes in "Components that hold in every theme".
4. **Charts follow the theme group.** Editorial themes use the editorial chart style; tool themes use the
   tool style. `assets/charts.js` implements both; the rules are in "Charts".
5. **Tune white space.** Adjust `--ws`, then measure with `scripts/measure_whitespace.js` (or the
   simulator's readout). Aim for roughly 55–68% white space in the content column of a tool page; Airy
   sits at the top of that band on purpose, Instrument at the bottom.
6. **Check before you ship.** Run `node scripts/check_theme.js <your css>` (contrast, chart-palette
   colour-vision checks). Take one screenshot and put it next to the matching reference screenshot. Walk the
   "Avoid" list. Then the quality floor: responsive to ~400px, visible focus, reduced motion, no clipped text.
7. **Hand over** with the theme name, the token block (or the CSS file), and the one thing you spent the
   boldness on.

## Pick a theme

| Theme | Group | Choose it when | Mood in one line |
|---|---|---|---|
| **Broadsheet** | Editorial | Prose is the content; research desks, briefs, reports people print or share | White paper, 2px ink rules instead of boxes, one red per view, serif display |
| **Ink** | Editorial | Same jobs as Broadsheet in low light; a dark sibling that swaps at runtime | Blue-black paper, warm off-white ink, red brightened one step |
| **Briefing** | Editorial | Chart-led dashboards and data briefings; analyst tools with working density | Cool grey canvas, white panels with a blue top bar, navy sidebar, red only as the data highlight |
| **Airy** | Tool | Consumer-grade tools, settings, onboarding, forms; approachability beats density | Google-inspired: one family, pills, outlined 12px cards, white page, ~60% white space |
| **Instrument** | Tool | Operator consoles, admin, developer tools, screens scanned all day | 13px base, tabular numerals, 6px radius, hairline panels, density as a feature |
| **Graphite** | Tool | Dark-mode-first tools, monitoring, developer products | Blue-grey neutrals (never pure black), periwinkle accent, 8px radius, borders as elevation |

Reading the brief: a *subject* usually points at a group (finance desk, newsroom, policy → editorial;
settings, ops, dev tools, consumer app → tool), and the *job* picks within it (read → Broadsheet/Ink,
scan charts → Briefing, operate many rows → Instrument, approach as a newcomer → Airy, live in it at
night → Graphite). When two fit, prefer the one whose signature move you can actually use on this page.

See the side-by-side: `assets/screenshots/sheet-all-desks.png`. Per theme, all four views:
`assets/screenshots/sheet-<theme>.png`. Full detail in `references/themes.md`.

## The token contract

Every theme defines all of these. Components read only these. Full roles and recipes:
`references/token-contract.md`.

| Group | Tokens | Role |
|---|---|---|
| Colour | `--bg --surface --surface-2 --ink --ink-2 --ink-3 --line --line-2` | page, panels, alt surface; three text steps; border and hairline |
| Accent | `--accent --accent-ink --accent-soft --accent-text` | the one brand colour: fill, text on the fill, tint, and the accent *as text* (dark themes brighten it so links stay readable) |
| Semantic | `--good --warn --bad` + `--good-bg --warn-bg --bad-bg --neutral-bg` | state, with soft fills for pills; separate from the accent and never used as a chart series |
| Series | `--s1 --s2 --s3 --s4` + `--chart-tab` | validated categorical chart colours in fixed order; the small tab above editorial chart titles |
| Type | `--font-display --font-text --font-ui`, `--display-weight --display-tracking`, `--fs-base --fs-sm --fs-xs --fs-h1 --fs-h2 --fs-head --fs-num --fs-prose --prose-lh` | display, reading and UI faces; the size scale |
| Shape | `--radius --radius-s --pill-radius --btn-radius --input-radius` | panels, controls, pills, buttons, fields |
| Panels | `--panel-bg --panel-border --panel-top --panel-shadow --table-top --table-head-bg --input-bg --input-border`, `--dialog-shadow --bad-btn` (optional) | how a panel, table and field are drawn: the editorial themes use a top rule, the tool themes a border |
| Sidebar | `--side-w --side-bg --side-ink --side-ink-2 --side-line --nav-active-bg --nav-active-ink --nav-mark --nav-mark-w --nav-radius --nav-ml` | the sidebar's own palette, the active mark (left bar vs pill vs tonal) and how far the item bleeds to the edge |
| Space | `--ws` → `--u: calc(8px * var(--ws))` | the white-space multiplier and the base unit everything is spaced in |

Give lengths real units even when zero (`0px`, not `0`): a unitless zero inside `calc()`/`max()`
silently invalidates the declaration and the layout collapses with no error.

## Components that hold in every theme

Each recipe is in `assets/theme-kit.css`; the point of listing them is the *why*.

- **Panels and tiles** read `--panel-bg/-border/-top/-shadow/--radius`. In Broadsheet that renders as a
  2px ink rule on top and nothing else; in Briefing a 3px accent bar; in the tool themes a 1px border.
  Never add a second mechanism (shadow + border + tint) to the same panel.
- **Sidebar nav** items bleed to the sidebar edge (`--nav-ml`) so the active mark sits flush: a red left bar
  in the editorial themes, a tonal pill in Airy, a quiet surface step in Instrument/Graphite. Text in the
  nav aligns with the logo mark. (The first version had the mark floating inside the padding, which reads
  as "|Desk" and looks like a margin bug. See `detail-<theme>-sidebar.png`.)
- **Buttons**: primary = accent fill + `--accent-ink`; secondary = outline; ghost = `--accent-text`;
  danger = `--bad-btn` (deeper than `--bad` on dark themes) with white text. Radius from `--btn-radius`.
- **Stat tiles**: label (sentence case), value in the UI sans (never the serif; serif numerals read as
  decoration), delta coloured by direction × whether up is good, 12-point sparkline in `--ink-3` with the
  current period in the accent.
- **Tables**: `--table-top` (2px ink in Broadsheet), header text in `--ink-2` at `--fs-xs`, hairline rows,
  `tabular-nums` on numeric columns, hover row in `--surface-2`.
- **Pills** carry state with a soft fill and a shape (dot / diamond / square) as well as colour, so state is
  never colour-alone. Semantic colours, not the accent.
- **Fields**: `--input-bg/-border/-radius`; error state turns the border and help text `--bad`.
- **Alerts** use a 3px left border in the semantic colour and an icon glyph coloured `--bg`, which is right
  on both light and dark. **Toasts** invert (`--ink` background, `--bg` text). **Dialogs** use
  `--dialog-shadow`: the editorial themes substitute a 1px ink outline for the drop shadow.
- **Article view** (`.article`): kicker in `--accent-text`, headline in the display face with
  `text-wrap: balance`, standfirst in the reading face, prose at 65ch. Drop cap only in Broadsheet/Ink.

## Charts

Two styles, chosen by theme group. Rules that hold in both (from the data-viz method the palettes were
validated with): 2px lines, 8px end markers with a 2px surface ring, hairline solid gridlines, a legend
whenever there are two or more series, text in text tokens (never in series colours), one axis, bars at
most 24px with a 4px rounded data end and a square baseline, a hover crosshair and tooltip.

- **Editorial** (Broadsheet, Ink, Briefing): a short tab in `--chart-tab` above the title, y labels on the
  *right* sitting on the gridline, no y-axis line, a baseline only, year ticks, series named inside the plot
  where they are farthest apart, a "Sources:" line underneath.
- **Tool** (Airy, Instrument, Graphite): y labels on the left, legend row with swatches above, values at
  the line ends, evenly spaced x labels.

`assets/charts.js` draws line, column and sparkline in both styles from the tokens. Full rules and the
series palettes (all six pass colour-vision separation and contrast): `references/charts.md`.
Compare with `detail-<theme>-chart.png` and `detail-<theme>-chart-hover.png`.

## White space

"60% white space" is a rule of thumb, cousin of the 60-30-10 colour rule (60% dominant neutral). Treat it as
a direction: on a tool page, about 55–68% of the content column should be empty once text line boxes,
controls, charts and images are counted as ink. Reach it with the spacing multiplier, not by deleting
content: `--ws` scales every padding, gap and margin at once (Airy ships at 1.4, Instrument at 0.8, the
rest at 1). Measure rather than guess: `node scripts/measure_whitespace.js page.html --selector .main
--overlay ink.png` prints the estimate and draws what it counted. Method, counting rules and worked
numbers: `references/white-space.md`; examples `overlay-*.png` and `spacing-airy-x*.png`.

## Quality floor

Responsive down to ~400px (sidebar becomes a scrolling strip; grids stack), visible `:focus-visible`
outline in the accent, `prefers-reduced-motion` respected, nothing clipped, body text ≥ 4.5:1 and
muted text ≥ 3:1 on its background, chart marks ≥ 3:1 on the surface, `color-scheme` set so native
controls match the theme. `scripts/check_theme.js` checks the contrast and chart-palette parts; the rest
you see in the screenshot. Checklist: `references/checklist.md`.

## Avoid (the generic defaults)

These appear in generated UI regardless of subject. Use any of them only when the brief asks for it.
Side-by-side: `assets/screenshots/anti-pattern-generic-vs-broadsheet.png`; detail in
`references/anti-patterns.md`.

- Warm cream page + serif display + terracotta accent (the single most common generated look).
- Near-black page with one acid-green or vermilion accent; tinted blacks (#0b0b0b, #111) standing in for black.
- The SaaS card kit: everything in identical rounded cards with the same soft grey shadow and one radius for all.
- Template chrome: tracked-out ALL-CAPS eyebrow labels above headings, "A · B · C" meta strings, an arrow
  appended to every button, a monospace face for small labels, one accented or italic word in a headline.
- Gradient washes as decoration; fade-and-slide-up on every section; hover transitions on every card.
- Numbered markers 01/02/03 on content that is not a sequence.
- A hero number in the display serif; serif numerals anywhere in data.
- Shadows in the editorial themes; more than one red element per editorial view; pure black in the dark themes.

## Theme token blocks

The same values live in `assets/theme-kit.css` with the component CSS; copy from there when you can.
These blocks are complete on their own if the file is not available.

```css
[data-theme="broadsheet"]{ color-scheme:light;
  --bg:#ffffff; --surface:#ffffff; --surface-2:#f3f3f0; --ink:#111111; --ink-2:#4a4a4a; --ink-3:#7a7a7a; --line:#d3d3ce; --line-2:#e8e8e3;
  --accent:#d62b1f; --accent-ink:#ffffff; --accent-soft:#fbe9e7; --accent-text:#d62b1f;
  --good:#1e7b46; --warn:#a5600f; --bad:#b3261e; --good-bg:#e6f3ec; --warn-bg:#fbefdc; --bad-bg:#fbe7e5; --neutral-bg:#efefeb;
  --s1:#1f5fa6; --s2:#d62b1f; --s3:#0f8f80; --s4:#b58a1c; --chart-tab:var(--accent);
  --font-display:"Fraunces",Georgia,"Times New Roman",serif; --font-text:"Source Serif 4",Georgia,serif; --font-ui:"Source Sans 3","Helvetica Neue",Arial,sans-serif;
  --display-weight:600; --display-tracking:-0.01em;
  --fs-base:15px; --fs-sm:13px; --fs-xs:12px; --fs-h1:30px; --fs-h2:18px; --fs-head:42px; --fs-num:28px; --fs-prose:17px; --prose-lh:1.55;
  --radius:0; --radius-s:0; --pill-radius:0; --btn-radius:0; --input-radius:0;
  --panel-bg:transparent; --panel-border:0; --panel-top:2px solid var(--ink); --panel-shadow:none; --table-top:2px solid var(--ink); --table-head-bg:transparent;
  --input-bg:#ffffff; --input-border:#8a8a8a; --dialog-shadow:0 0 0 1px var(--ink);
  --side-w:220px; --side-bg:#ffffff; --side-ink:#111111; --side-ink-2:#4a4a4a; --side-line:#d3d3ce;
  --nav-active-bg:transparent; --nav-active-ink:#111111; --nav-mark:var(--accent); --nav-mark-w:3px; --nav-radius:0; --nav-ml:calc(var(--u) * -1.5);
  --swatch-w:12px; --swatch-h:3px; --swatch-r:0; --ws:1; }

[data-theme="ink"]{ color-scheme:dark;
  --bg:#0f1419; --surface:#161c23; --surface-2:#1d242d; --ink:#edebe6; --ink-2:#b3b0a9; --ink-3:#7f7c75; --line:#2c343e; --line-2:#222a33;
  --accent:#dc3328; --accent-ink:#ffffff; --accent-soft:#3a1d1b; --accent-text:#f0655b;
  --good:#4cc38a; --warn:#e3a92b; --bad:#ff6b61; --good-bg:rgba(76,195,138,.14); --warn-bg:rgba(227,169,43,.14); --bad-bg:rgba(255,107,97,.14); --neutral-bg:#232b35;
  --s1:#3f86d6; --s2:#e34b40; --s3:#2aa58f; --s4:#bd8526; --chart-tab:var(--accent);
  --font-display:"Newsreader",Georgia,serif; --font-text:"Newsreader",Georgia,serif; --font-ui:"Source Sans 3","Helvetica Neue",Arial,sans-serif;
  --display-weight:500; --display-tracking:-0.005em;
  --fs-base:15px; --fs-sm:13px; --fs-xs:12px; --fs-h1:30px; --fs-h2:18px; --fs-head:42px; --fs-num:28px; --fs-prose:17.5px; --prose-lh:1.6;
  --radius:0; --radius-s:0; --pill-radius:0; --btn-radius:0; --input-radius:0;
  --panel-bg:var(--surface); --panel-border:1px solid var(--line); --panel-top:1px solid var(--line); --panel-shadow:none; --table-top:1px solid var(--ink-3); --table-head-bg:transparent;
  --input-bg:#121820; --input-border:#3a434e; --dialog-shadow:0 0 0 1px var(--ink-3); --bad-btn:#c73a2e;
  --side-w:220px; --side-bg:#0f1419; --side-ink:#edebe6; --side-ink-2:#b3b0a9; --side-line:#2c343e;
  --nav-active-bg:transparent; --nav-active-ink:#edebe6; --nav-mark:var(--accent); --nav-mark-w:3px; --nav-radius:0; --nav-ml:calc(var(--u) * -1.5);
  --swatch-w:12px; --swatch-h:3px; --swatch-r:0; --ws:1; }

[data-theme="briefing"]{ color-scheme:light;
  --bg:#edf0f3; --surface:#ffffff; --surface-2:#f4f6f8; --ink:#0f1b2d; --ink-2:#45536a; --ink-3:#74819a; --line:#d3dae3; --line-2:#e6ebf0;
  --accent:#1d5a9a; --accent-ink:#ffffff; --accent-soft:#e2ecf7; --accent-text:#1d5a9a; --highlight:#d62b1f;
  --good:#187045; --warn:#8f5d06; --bad:#b8321f; --good-bg:#e4f2ea; --warn-bg:#faefd9; --bad-bg:#f9e5e1; --neutral-bg:#e9edf2;
  --s1:#1d5a9a; --s2:#d62b1f; --s3:#4c9bd6; --s4:#b38600; --chart-tab:var(--highlight);
  --font-display:"Source Serif 4",Georgia,serif; --font-text:"Source Serif 4",Georgia,serif; --font-ui:"IBM Plex Sans","Helvetica Neue",Arial,sans-serif;
  --display-weight:600; --display-tracking:-0.01em;
  --fs-base:14px; --fs-sm:13px; --fs-xs:12px; --fs-h1:26px; --fs-h2:17px; --fs-head:36px; --fs-num:26px; --fs-prose:16.5px; --prose-lh:1.55;
  --radius:2px; --radius-s:2px; --pill-radius:2px; --btn-radius:2px; --input-radius:2px;
  --panel-bg:#ffffff; --panel-border:0; --panel-top:3px solid var(--accent); --panel-shadow:0 1px 2px rgba(15,27,45,.08); --table-top:1px solid var(--line); --table-head-bg:var(--surface-2);
  --input-bg:#ffffff; --input-border:#b7c2d0;
  --side-w:224px; --side-bg:#0f1b2d; --side-ink:#ffffff; --side-ink-2:#aab6c8; --side-line:#0f1b2d;
  --nav-active-bg:rgba(255,255,255,.08); --nav-active-ink:#ffffff; --nav-mark:var(--highlight); --nav-mark-w:3px; --nav-radius:0; --nav-ml:calc(var(--u) * -1.5);
  --swatch-w:12px; --swatch-h:3px; --swatch-r:0; --ws:1; }

[data-theme="airy"]{ color-scheme:light;
  --bg:#ffffff; --surface:#ffffff; --surface-2:#f1f3f4; --ink:#202124; --ink-2:#5f6368; --ink-3:#80868b; --line:#dadce0; --line-2:#e8eaed;
  --accent:#1a73e8; --accent-ink:#ffffff; --accent-soft:#e8f0fe; --accent-text:#1a73e8;
  --good:#188038; --warn:#b06000; --bad:#c5221f; --good-bg:#e6f4ea; --warn-bg:#fef7e0; --bad-bg:#fce8e6; --neutral-bg:#f1f3f4;
  --s1:#1a73e8; --s2:#e8710a; --s3:#9334e6; --s4:#0e9aa8; --chart-tab:var(--accent);
  --font-display:"Figtree",Roboto,system-ui,sans-serif; --font-text:"Figtree",Roboto,system-ui,sans-serif; --font-ui:"Figtree",Roboto,system-ui,sans-serif;
  --display-weight:600; --display-tracking:-0.01em;
  --fs-base:14px; --fs-sm:13px; --fs-xs:12px; --fs-h1:28px; --fs-h2:16px; --fs-head:36px; --fs-num:32px; --fs-prose:15px; --prose-lh:1.65;
  --radius:12px; --radius-s:8px; --pill-radius:999px; --btn-radius:999px; --input-radius:8px;
  --panel-bg:#ffffff; --panel-border:1px solid var(--line); --panel-top:1px solid var(--line); --panel-shadow:none; --table-top:0; --table-head-bg:transparent;
  --input-bg:#ffffff; --input-border:#dadce0;
  --side-w:236px; --side-bg:#ffffff; --side-ink:#202124; --side-ink-2:#5f6368; --side-line:#ffffff;
  --nav-active-bg:#e8f0fe; --nav-active-ink:#1967d2; --nav-mark:transparent; --nav-mark-w:0px; --nav-radius:0 999px 999px 0; --nav-ml:calc(var(--u) * -1.5);
  --swatch-w:10px; --swatch-h:10px; --swatch-r:50%; --ws:1.4; }

[data-theme="instrument"]{ color-scheme:light;
  --bg:#f6f6f8; --surface:#ffffff; --surface-2:#f0f0f4; --ink:#15161a; --ink-2:#5a5d68; --ink-3:#8a8d98; --line:#e1e2e8; --line-2:#ebecf0;
  --accent:#4f46e5; --accent-ink:#ffffff; --accent-soft:#ecebfc; --accent-text:#4f46e5;
  --good:#15803d; --warn:#a16207; --bad:#d82222; --good-bg:#e7f6ec; --warn-bg:#fdf3dc; --bad-bg:#fdeaea; --neutral-bg:#ededf2;
  --s1:#4f46e5; --s2:#e5582a; --s3:#0d9488; --s4:#c026d3; --chart-tab:var(--accent);
  --font-display:"Manrope",system-ui,sans-serif; --font-text:"Manrope",system-ui,sans-serif; --font-ui:"Manrope",system-ui,sans-serif;
  --display-weight:700; --display-tracking:-0.015em;
  --fs-base:13px; --fs-sm:12px; --fs-xs:11px; --fs-h1:20px; --fs-h2:14px; --fs-head:26px; --fs-num:22px; --fs-prose:14px; --prose-lh:1.6;
  --radius:6px; --radius-s:4px; --pill-radius:4px; --btn-radius:5px; --input-radius:5px;
  --panel-bg:#ffffff; --panel-border:1px solid var(--line); --panel-top:1px solid var(--line); --panel-shadow:0 1px 2px rgba(20,22,30,.04); --table-top:0; --table-head-bg:var(--surface-2);
  --input-bg:#ffffff; --input-border:#d6d7df;
  --side-w:200px; --side-bg:#f6f6f8; --side-ink:#15161a; --side-ink-2:#5a5d68; --side-line:#e1e2e8;
  --nav-active-bg:#e6e6ee; --nav-active-ink:#15161a; --nav-mark:transparent; --nav-mark-w:0px; --nav-radius:4px; --nav-ml:0px;
  --swatch-w:8px; --swatch-h:8px; --swatch-r:2px; --ws:0.8; }

[data-theme="graphite"]{ color-scheme:dark;
  --bg:#15181d; --surface:#1c2027; --surface-2:#242931; --ink:#e7e9ec; --ink-2:#9ba3ae; --ink-3:#6b7380; --line:#2c323b; --line-2:#262c34;
  --accent:#5563d8; --accent-ink:#ffffff; --accent-soft:rgba(85,99,216,.2); --accent-text:#8b9bff;
  --good:#3fb950; --warn:#d29922; --bad:#f85149; --good-bg:rgba(63,185,80,.15); --warn-bg:rgba(210,153,34,.15); --bad-bg:rgba(248,81,73,.15); --neutral-bg:#2a3039;
  --s1:#6b7ae8; --s2:#1aa88c; --s3:#d9671c; --s4:#c84fab; --chart-tab:var(--accent);
  --font-display:"DM Sans",system-ui,sans-serif; --font-text:"DM Sans",system-ui,sans-serif; --font-ui:"DM Sans",system-ui,sans-serif;
  --display-weight:600; --display-tracking:-0.01em;
  --fs-base:13.5px; --fs-sm:12.5px; --fs-xs:11.5px; --fs-h1:22px; --fs-h2:15px; --fs-head:28px; --fs-num:24px; --fs-prose:14.5px; --prose-lh:1.6;
  --radius:8px; --radius-s:6px; --pill-radius:999px; --btn-radius:6px; --input-radius:6px;
  --panel-bg:var(--surface); --panel-border:1px solid var(--line); --panel-top:1px solid var(--line); --panel-shadow:none; --table-top:0; --table-head-bg:transparent;
  --input-bg:#15181d; --input-border:#3a414c; --bad-btn:#c93434;
  --side-w:216px; --side-bg:#15181d; --side-ink:#e7e9ec; --side-ink-2:#9ba3ae; --side-line:#2c323b;
  --nav-active-bg:#242931; --nav-active-ink:#e7e9ec; --nav-mark:transparent; --nav-mark-w:0px; --nav-radius:6px; --nav-ml:0px;
  --swatch-w:10px; --swatch-h:10px; --swatch-r:50%; --ws:1; }
```

Google Fonts, one link for all six:
`https://fonts.googleapis.com/css2?family=DM+Sans:opsz,wght@9..40,400;9..40,500;9..40,600&family=Figtree:wght@400;500;600;700&family=Fraunces:opsz,wght@9..144,500;9..144,600&family=IBM+Plex+Sans:wght@400;500;600&family=Manrope:wght@400;500;600;700&family=Newsreader:opsz,wght@6..72,400;6..72,500;6..72,600&family=Source+Sans+3:wght@400;500;600;700&family=Source+Serif+4:opsz,wght@8..60,400;8..60,600&display=swap`
(load only the families your theme uses).

## How this relates to frontend-ui-modern

They are two different skills for two different jobs, and they should stay that way:

| | frontend-ui-economist (this skill) | frontend-ui-modern |
|---|---|---|
| Stack | plain HTML/CSS/SVG: artifacts, static pages, prototypes, any server-rendered template | shadcn/ui on Tailwind + React (Next.js, Vite, TanStack, …) |
| Look | editorial (Economist-inspired), Google-airy, dense console, dark-first | shadcn styles + 20 dotfile themes (Catppuccin, Tokyo Night, Dracula, Nord, …) |
| Charts | `assets/charts.js`, editorial and tool styles | Recharts through shadcn `ChartContainer` |
| Method | token contract, white-space measurement, anti-pattern checks | simulate → decide → build with the CLI → mockup gates |

If the request names shadcn, Tailwind, React components or a dotfile theme, or the project has a
`components.json`, hand over to frontend-ui-modern. If it names an editorial, newspaper, report or
data-briefing look, a Google-style airy tool, or is built as plain HTML/CSS, this skill applies.

A theme from here can still feed a shadcn build. Map the tokens onto the shadcn contract and paste them
as its `:root` / `.dark` pair (lightness steps stay as they are; shadcn tokens accept hex):

| this skill | shadcn token |
|---|---|
| `--bg` | `--background` |
| `--ink` | `--foreground`, `--card-foreground`, `--popover-foreground` |
| `--surface` | `--card`, `--popover` |
| `--surface-2` | `--secondary`, `--muted`, `--accent`, `--sidebar-accent` |
| `--ink-2` | `--muted-foreground`, `--secondary-foreground` |
| `--line` | `--border`, `--input`, `--sidebar-border` |
| `--accent` / `--accent-ink` | `--primary`, `--ring`, `--sidebar-primary` / `--primary-foreground` |
| `--bad` | `--destructive` |
| `--s1 … --s4` | `--chart-1 … --chart-4` (`--chart-5`: pick a fifth with `scripts/check_theme.js`) |
| `--side-bg` / `--side-ink` | `--sidebar` / `--sidebar-foreground` |
| `--radius` | `--radius` (Broadsheet 0 → shadcn radius `none`; Airy 12px → `large`) |

The editorial panel rules (`--panel-top`), the sidebar mark (`--nav-mark`) and the chart tab have no shadcn
equivalent; they are what make the editorial themes editorial, so expect a shadcn port of Broadsheet to
read as "shadcn with a serif" unless you add those three by hand.

## Files in this skill

| Path | What it is | Read it when |
|---|---|---|
| `references/themes.md` | Each theme in depth: tokens, use/signature/avoid, every screenshot of it | you have picked a theme |
| `references/token-contract.md` | Every token's role and the component recipes written against them | you build a component not covered above, or extend a theme |
| `references/charts.md` | Both chart styles, the mark rules, the validated series palettes | the page has a chart |
| `references/white-space.md` | The 60% heuristic, what counts as ink, how to measure, worked numbers | the page feels dense or empty |
| `references/anti-patterns.md` | The generic-default tells with the before/after screenshot | before shipping, always |
| `references/checklist.md` | The pre-ship checklist in one place | before shipping, always |
| `references/further-reading.md` | Sources: The Economist's public chart style guide, Material, WCAG, the typefaces, the CVD model | you want the provenance |
| `assets/theme-kit.css` | All six token blocks + the component CSS | step 2 of the workflow |
| `assets/charts.js` | Line, column and sparkline in both chart styles | step 4 |
| `assets/starter.html` | A minimal themed tool page with a theme switcher, wired to the two files above | starting a new page |
| `assets/theme-simulator.html` | The full simulator: one app, six themes, four views, white-space readout, token export | you want to browse or show the themes |
| `assets/screenshots/` | 150+ reference renders; `README.md` there is the index | comparing your output to the reference |
| `scripts/check_theme.js` | Contrast + chart-palette colour-vision checks for a theme block (no deps) | step 6 |
| `scripts/measure_whitespace.js` | White-space estimate + ink overlay for any page (needs Playwright) | step 5 |
