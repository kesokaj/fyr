# The token contract

Every theme sets every token below; every component reads only tokens. That is the whole trick: a page
written against the contract changes personality by swapping one block, and a component never needs to
know which theme it is in. When you add a theme, fill the whole contract and run `scripts/check_theme.js`.
When you add a component, write it against tokens and check it in at least one editorial and one tool
theme (they stress different things: rules vs borders, zero vs large radius, light vs dark).

Reference implementation of everything here: `../assets/theme-kit.css`.

## Units

- `--ws` is the white-space multiplier; `--u: calc(8px * var(--ws))` is the base unit. Space in multiples
  of `--u` (`calc(var(--u) * 2)`), and give the smallest paddings a floor with `max(6px, calc(var(--u) * .8))`
  so a low multiplier cannot crush controls.
- Give lengths units even when zero: `--nav-mark-w: 0px`, `--nav-ml: 0px`. A unitless `0` inside
  `calc()` or `max()` makes the whole declaration invalid at computed-value time; the browser reports
  nothing and the property falls back to its initial value (that is how a sidebar's text ended up flush
  against the viewport edge during the build).
- Font sizes do not scale with `--ws`; density comes from spacing, not from shrinking type.

## Colour

| Token | Role | Notes |
|---|---|---|
| `--bg` | page background | set `color-scheme` next to it so native controls match |
| `--surface` | panels, inputs, tooltips, the chart surface | editorial light themes set it equal to `--bg` |
| `--surface-2` | alt surface: hover rows, selected menu items, table header bands, secondary buttons | one step off `--surface` |
| `--ink` | primary text | ≥ 4.5:1 on `--bg` and `--surface` |
| `--ink-2` | secondary text: subtitles, table headers, legends | ≥ 4.5:1 |
| `--ink-3` | muted text: sources, help text, placeholders; sparkline stroke | ≥ 3:1 |
| `--line` | borders around fields, panel borders, baselines, section rules | |
| `--line-2` | hairlines: table rows, chart gridlines, list dividers | one step lighter than `--line` |

## Accent

| Token | Role | Notes |
|---|---|---|
| `--accent` | the one brand colour as a fill: primary button, active nav, switch on, focus ring, logo mark | |
| `--accent-ink` | text on an accent fill | ≥ 4.5:1 on `--accent` |
| `--accent-soft` | tint: selected chips, nav pill (Airy), selected menu item | |
| `--accent-text` | the accent used *as text*: ghost buttons, kickers, links, selected chip labels | light themes reuse `--accent`; dark themes brighten it, because no single red or blue passes both as a fill under white text and as text on a dark page |
| `--chart-tab` | the short bar above editorial chart titles | Briefing points it at `--highlight` (red) while its accent is blue |

## Semantic

| Token | Role |
|---|---|
| `--good --warn --bad` | state as text (deltas, help text in error) and as the pill/alert colour; ≥ 4.5:1 on `--bg` |
| `--good-bg --warn-bg --bad-bg --neutral-bg` | soft fills behind pills; on dark themes these are rgba tints of the colour |
| `--bad-btn` (optional) | danger button fill when `--bad` is too light for white text (dark themes) |

Semantic colours are separate from the accent and are never used as chart series: a reader who has learned
"red means alert" should not meet red as "series 2" on the same page unless the theme makes red its data
highlight on purpose (Broadsheet, Briefing), in which case `--bad` is a different, deeper red.

## Chart series

`--s1 … --s4` are categorical colours in fixed order. Assign in order and never cycle; a fifth series folds
into "Other" or becomes small multiples. All six palettes pass: OKLCH lightness inside the mode's band,
chroma ≥ 0.10, adjacent-pair separation under simulated protanopia/deuteranopia (ΔE ≥ 8, OKLab × 100),
normal-vision separation ≥ 15, and ≥ 3:1 against `--surface`. Dark themes have their own validated set;
they are not inversions of the light ones. Checked by `scripts/check_theme.js`.

## Type

| Token | Role |
|---|---|
| `--font-display` | headlines, the page h1, the logo, dialog titles, drop caps |
| `--font-text` | reading text: article prose, standfirst, the brief panel |
| `--font-ui` | everything else, including every number |
| `--display-weight --display-tracking` | the display face's weight and letter-spacing |
| `--fs-base --fs-sm --fs-xs` | UI text, small text, labels |
| `--fs-h1 --fs-h2 --fs-head` | page heading, section heading, article headline |
| `--fs-num` | stat-tile value |
| `--fs-prose --prose-lh` | reading text size and line height (serifs get a little more) |

Numbers always sit in `--font-ui`. In tiles use proportional figures (a `121` in tabular figures looks
loose at 28px); in table columns and axis ticks use `font-variant-numeric: tabular-nums`.

## Shape

| Token | Applies to |
|---|---|
| `--radius` | panels, tiles, dialogs, empty states |
| `--radius-s` | controls, menus, alerts, tooltips, small swatches |
| `--pill-radius` | pills, chips, badges, the switch track |
| `--btn-radius` | buttons (Airy: 999px pill) |
| `--input-radius` | text fields and selects |

## Panels, tables, fields

| Token | Role |
|---|---|
| `--panel-bg` | panel fill (`transparent` in Broadsheet: the page is the panel) |
| `--panel-border` | full border shorthand, or `0` |
| `--panel-top` | the top edge, set separately so editorial themes can draw a rule there (`2px solid var(--ink)`, `3px solid var(--accent)`) |
| `--panel-shadow` | `none` in editorial and Airy; a 1px 4–8% shadow at most elsewhere |
| `--table-top` | the table's top rule (2px ink in Broadsheet, 1px muted in Ink, 0 in tool themes) |
| `--table-head-bg` | header band (`--surface-2` in Briefing and Instrument) |
| `--input-bg --input-border` | fields; dark themes use a slightly darker field than the surface |
| `--dialog-shadow` (optional) | editorial themes replace the drop shadow with `0 0 0 1px var(--ink)` |

## Sidebar

| Token | Role |
|---|---|
| `--side-w` | width (200–236px) |
| `--side-bg --side-ink --side-ink-2 --side-line` | the sidebar's own palette (Briefing: navy on a light page) |
| `--nav-active-bg --nav-active-ink` | active item fill and text |
| `--nav-mark --nav-mark-w` | the left bar on the active item (3px red in editorial themes, `0px` elsewhere) |
| `--nav-radius` | `0`, `4–6px`, or `0 999px 999px 0` for Airy's half-pill |
| `--nav-ml` | negative margin that bleeds the item to the sidebar edge so the mark or pill touches it; the recipe adds the same amount back as padding so text still aligns with the logo |
| `--swatch-w --swatch-h --swatch-r` | legend swatch shape: a 12×3 line in editorial themes, a dot in tool themes |

## Component recipes (reading only tokens)

```css
.panel{background:var(--panel-bg);border:var(--panel-border);border-top:var(--panel-top);border-radius:var(--radius);box-shadow:var(--panel-shadow);padding:calc(var(--u)*2) calc(var(--u)*2.5) calc(var(--u)*2.5)}
.btn{font:inherit;font-weight:600;font-size:var(--fs-sm);padding:max(7px,calc(var(--u)*1)) max(12px,calc(var(--u)*1.75));border-radius:var(--btn-radius);border:1px solid transparent;background:var(--surface-2);color:var(--ink)}
.btn.primary{background:var(--accent);color:var(--accent-ink)}
.btn.secondary{background:transparent;border-color:var(--line)}
.btn.ghost{background:transparent;color:var(--accent-text,var(--accent))}
.btn.danger{background:var(--bad-btn,var(--bad));color:#fff}
.nav a{padding:max(6px,calc(var(--u)*.8)) calc(var(--u)*1.25);border-left:var(--nav-mark-w) solid transparent;border-radius:var(--nav-radius);margin-left:var(--nav-ml);padding-left:calc(var(--u)*1.25 + max(0px, var(--nav-ml) * -1) - var(--nav-mark-w));color:var(--side-ink-2)}
.nav a.is-active{background:var(--nav-active-bg);color:var(--nav-active-ink);border-left-color:var(--nav-mark);font-weight:600}
.pill{display:inline-flex;gap:5px;padding:1px 8px;border-radius:var(--pill-radius);font-size:var(--fs-xs);font-weight:600;background:var(--neutral-bg);color:var(--ink-2)}
.pill.good{background:var(--good-bg);color:var(--good)} .pill.warn{background:var(--warn-bg);color:var(--warn)} .pill.bad{background:var(--bad-bg);color:var(--bad)}
.table{border-top:var(--table-top)} .table th{color:var(--ink-2);font-size:var(--fs-xs);border-bottom:1px solid var(--line);background:var(--table-head-bg)} .table td{border-bottom:1px solid var(--line-2)} .table .num{text-align:right;font-variant-numeric:tabular-nums}
.input{background:var(--input-bg);border:1px solid var(--input-border);border-radius:var(--input-radius);color:var(--ink)} .field.error .input{border-color:var(--bad)}
.tile .value{font-size:var(--fs-num);font-weight:600;font-family:var(--font-ui)}
.headline{font-family:var(--font-display);font-size:var(--fs-head);font-weight:var(--display-weight);letter-spacing:var(--display-tracking);line-height:1.08;text-wrap:balance;max-width:22ch}
.prose{font-family:var(--font-text);font-size:var(--fs-prose);line-height:var(--prose-lh);max-width:65ch}
.ui :focus-visible{outline:2px solid var(--accent);outline-offset:2px}
```

The full set, including tiles, legend, chart text, alerts, toast, menu, dialog, empty state, tabs,
breadcrumbs, pagination, switch, filter chips and the responsive rules, is in `../assets/theme-kit.css`.

## Adding a seventh theme

1. Copy the nearest block and rename `[data-theme="…"]`.
2. Decide the four moves that make it a theme: display face, radius, panel mechanism (rule / border /
   tint), accent. Everything else can stay close to its parent.
3. Pick series colours by the job (identity in fixed order), then run `node scripts/check_theme.js your.css`
   and fix every FAIL. Text contrast fails are fixed by moving the text token, not the background.
4. Render the simulator with the new block (`assets/theme-simulator.html` reads the same tokens; add a
   `.stage[data-skin="…"]` block and a `THEMES` entry) and compare the four views to the nearest theme's
   screenshots.
