# Workflow: Discover → Simulate → Decide → Build → Mockup → Sign-off

The two gates exist because UI work is expensive to redo and cheap to preview. A simulation costs minutes and
prevents installing the wrong layout/theme; a mockup catches the "it looked fine in my head" failures (dark mode
leaks, mobile overflow, hard-coded colors) before the operator finds them. Keep both proportional to the task:
a new app gets 2–3 full directions; "add a settings page" gets one screen in two layouts inside the existing theme.

## Phase 0 — Discover (5 minutes, no questions the simulation can answer better)

1. Project facts, in this order of preference:
   - `npx shadcn@latest info --json` (needs `components.json`): framework, tailwind version, `style` (e.g. `base-nova`),
     `base` (`base` | `radix` | `aria`), `iconLibrary`, `aliases`, installed components, `tailwindCssFile`, `preset`.
   - No `components.json`? Note the stack (Next.js / Vite / TanStack Start / React Router / Astro / Laravel), package
     manager, whether Tailwind v4 is present, and whether there is an existing design language to respect.
2. The job: which screens, which data (real field names, realistic values, counts), which user roles, which states
   (loading, empty, error, success), which devices matter.
3. Existing taste signals: a current theme, a brand color, a dotfile theme the operator mentions ("I use Catppuccin
   everywhere"), a preset code, a DESIGN.md. If the operator named a theme, the simulation still shows it next to
   one alternative — a side-by-side is how people discover they wanted the other one.
4. Ask at most two questions here, only for things a simulation cannot resolve (e.g. "is this internal or
   customer-facing?", "must it match an existing app?"). Everything visual is answered by the simulation.

## Phase 1 — Simulate (the first gate)

Produce `ui-sim/index.html` from the template, then show it:

```bash
node <skill>/scripts/sim.mjs init ui-sim --project "<Project>" --themes default,<candidate-1>,<candidate-2>
```

The template is a multi-view prototype: shells (`<section class="sim-screen" data-direction="A|B">`: inset sidebar, top nav) ×
views (`<template data-view>`: overview, analytics, customers, customer detail, five settings tabs, login, signup, otp, onboarding, 404) ×
states (normal/loading/empty/error) × overlays (command, dialog, confirm, sheet, toast, mobile-nav). Nav items with `data-go` make it
clickable. Rewrite views/shells into the project's screens:

- **Directions differ in structure, not just color.** Good spreads: inset sidebar dashboard vs top-nav + tabs;
  dense data table vs card list; single-column form vs two-column form with summary rail. Theme and style are
  switchable in the toolbar, so a direction does not need to exist per theme.
- **Use the project's real content**: real nav labels, real column names, plausible numbers and names. Lorem ipsum
  hides layout problems and makes the operator decide on vibes alone.
- **Include the hard states** at least once: a validation error, an empty state, a long label, a table with 6+ rows.
- **Map every region to what will be installed** in `data-notes` ("sidebar-07 + dashboard-01 cards + chart-area-interactive").
  That note is the contract for Phase 3.
- **Keep the simulator chrome** (toolbar, notes strip, stage). It is deliberately plain so nobody mistakes it for the design.
- Chart data: `data-chart="area|bar|line|donut|radial"` with `data-series="key:Label:--chart-1,..."` renders a Recharts-like chart; swap the
  `DATA` generator in the script for real-shaped data when the shape matters (sparse, negative, many series).
- Give every view its states where they exist (`.state-loading` skeletons, `.state-empty` Empty, `.state-error` Alert) and the overlays the
  flow needs (confirm before destructive actions, sheet for quick views, toast for feedback) — operators decide on those too.
- Pre-select the recommended shell/view/theme/style by setting the `<html>` attributes or by linking with
  `?dir=A&view=overview&theme=catppuccin&dark=1&style=nova&radius=default`.

Show it in whatever way the environment supports: open the file (Claude Code: `open ui-sim/index.html` / `xdg-open`),
publish it as an HTML artifact (claude.ai), or capture it with
`node <skill>/scripts/mockup.mjs ui-sim/index.html --themes a,b --views overview,customers,settings-general --states normal,empty --overlays none,dialog --modes light,dark --widths 1280,390 --full`
and send the PNGs. Capture every view the operator will decide on; the home screen alone hides most layout decisions (tables, forms, overlays, empty states).

Then say which direction you recommend and why (one line each for layout, theme, style, radius), list contrast
warnings from `node <skill>/scripts/theme.mjs check <theme>` that affect body text, and ask for the decision
(AskUserQuestion with one question per axis when available; otherwise plain text). The operator can also press
"copy decision" in the toolbar and paste it back.

Unattended? Take the recommended direction, write it to `ui-sim/DECISION.md` with `Status: PROVISIONAL`, and say so
at the top of the hand-off. Never skip the simulation itself — a provisional decision still needs the artifact the
operator will judge later.

## Phase 2 — Decide

Fill `ui-sim/DECISION.md` (sim.mjs creates the template) with: direction, theme id + light/dark variants, default
mode, style, radius, base library, icon library, fonts, blocks per screen, chart palette, extras tokens, scope,
out-of-scope, open questions. This file is the single source of truth for Phase 3 and the hand-off; update it when
the operator changes their mind, don't keep the history in chat.

## Phase 3 — Build

Order of operations (each step is verifiable before the next):

1. **Init or align the project.**
   - New: `npx shadcn@latest init --template <next|vite|start|react-router|astro|laravel> --preset <style> --base <base|radix|aria> --name <app> --no-monorepo -y`
     (preset code from shadcn/create if the operator has one: `--preset <code>`). `init` with neither `--preset` nor
     `--base` stops at an interactive prompt even with `-y`.
   - Existing: respect `components.json`. Switch style/preset only if the decision says so
     (`npx shadcn@latest apply <code>`, or `init --preset <style> --force --no-reinstall` + per-component `--diff`).
   - Dark mode: the Next.js/Vite templates ship a ThemeProvider; otherwise follow `references/shadcn-2026.md#dark-mode`.
2. **Apply the theme tokens.** Copy the chosen `:root` + `.dark` pair from `references/themes/<family>.css`
   (or `node <skill>/scripts/theme.mjs css <id> --extras`) into `tailwindCssFile`, replacing the generated `:root`/`.dark`
   blocks. Keep `@theme inline` as generated; add the `--color-success/-warning/-info` lines if extras are used.
   Multi-theme apps (user-switchable): use `--scope data-theme` output and set `data-theme` on `<html>` next to the
   `dark` class.
3. **Install blocks/components** named in DECISION.md: `npx shadcn@latest add sidebar-07 dashboard-01 login-04 chart-area-interactive ...`.
   Then read every added file: fix imports to the project's aliases, swap icon imports to the project's `iconLibrary`,
   and check composition against `references/components.md#code-rules`.
4. **Compose the screens** from the blocks: replace demo nav/data with the real ones, wire data, add the states
   (Skeleton for loading, Empty for empty, Alert/Field errors for errors, Toast/Sonner for feedback).
5. **Charts**: one `ChartConfig` per chart with `color: "var(--chart-n)"`, `ChartContainer className="min-h-[200px] w-full"`
   (or `aspect-auto h-[250px] w-full`), `ChartTooltip` + `ChartLegend`, `accessibilityLayer`. See `references/charts.md`.
6. **Typecheck and run**: `npx tsc --noEmit` (or the project's `lint`/`typecheck` script) and start the dev server.
7. **Proof trail.** Every milestone ends with a capture: `node <skill>/scripts/mockup.mjs http://localhost:3000/<route> --out ui-mockups/01-shell`
   (then `02-theme`, `03-<screen>`, …). Open the PNG and compare it with `references/screenshots/theme-<id>-<mode>-1280.png` for the chosen
   theme: page/card/sidebar tiers, primary button, chart colors, muted text, border weight. Fix mismatches before the next milestone.
   The rounds stay in the repo (or are listed in the hand-off) as evidence of what was built and when.

Code rules that matter most (full list in `references/components.md`): semantic tokens only (`bg-background`,
`text-muted-foreground`, never `bg-zinc-900` or a hex); `className` for layout, variants for looks; `flex gap-*` not
`space-y-*`; `size-*` for squares; forms are `FieldGroup > Field > FieldLabel + control + FieldDescription/FieldError`;
Base UI uses `render={<a/>}`, Radix uses `asChild`; every Dialog/Sheet/Drawer has a Title; icons inside Button use
`data-icon="inline-start|inline-end"` and no size classes.

## Phase 4 — Mockup (the second gate)

```bash
node <skill>/scripts/mockup.mjs http://localhost:3000/<route> --out ui-mockups/round-1 --widths 1440,390 --modes light,dark
```

Options: `--dark class` (default; adds `.dark` to `<html>` and primes next-themes/vite storage keys),
`--dark media` (prefers-color-scheme), `--dark attr=data-theme:dark`, `--wait "[data-slot=sidebar]"`, `--full`,
`--query "tab=members"`. The script flags horizontal overflow and console errors per capture and writes
`ui-mockups/round-1/index.html` (contact sheet + checklist). Capture each screen, tab, overlay and state in scope (one URL or `--query` per tab/state), in both modes, at both widths.

If the app cannot run where you are (no dev server, missing env), update the simulation to the built state and
capture that instead — and say so explicitly; a simulation screenshot is not evidence the code works.

Review the captures yourself before showing them (open the PNGs, and keep the reference render for the chosen theme open next to them):

- Matches DECISION.md: layout, theme tokens (no default-shadcn gray where the theme should show), style, radius.
- Light **and** dark read correctly: no white cards in dark mode, no hard-coded text colors, charts recolored.
- Mobile: no overflow, nav reachable (SidebarTrigger / Sheet), tap targets ≥ 40px, tables scroll or collapse.
- States present where they matter; focus rings visible (tab through once).
- Contrast: body text ≥ 4.5:1, muted text ≥ 4.5:1 where it carries meaning (3:1 for decorative); the theme's
  `check` warnings were either resolved or accepted by the operator.
- Numbers use `tabular-nums`; dates/currency formatted; truncation with `truncate`, not overflow.

Send the contact sheet (or the PNGs) with a short list: what matches, what deviates from the simulation and why,
anything left open. Ask for sign-off. Fix → recapture into `round-2/` → repeat until the operator says done.

## Phase 5 — Sign-off and hand-off

Done means: operator approved the mockup; DECISION.md status is `APPROVED`; `ui-sim/` and `ui-mockups/` are
gitignored or committed deliberately; the hand-off lists the files added/changed, the theme file used, how to
switch themes/modes, and the two commands to regenerate the simulation and the mockups.

## Templates

**Simulation brief (post with the simulation):**

```
Simulation ready: ui-sim/index.html (or <artifact link>)
A — <title>: <layout>, blocks <…>            ← recommended because <reason>
B — <title>: <layout>, blocks <…>
Themes to compare: <id> (<light>/<dark>), <id> …   Style: <nova|…>   Radius: <default|…>
Contrast notes: <theme>: muted text 4.0:1 on background (below AA) — fix or accept?
Decide: direction · theme · default mode · style · radius   (toolbar → "copy decision")
```

**Sign-off request (post with the mockups):**

```
Mockups: ui-mockups/round-1/index.html — <n> captures (light/dark × 1440/390)
Matches decision: <list>
Deviations: <what, why, proposal>
Checks: overflow none · console clean · contrast ok except <…> · states: loading/empty/error present
Approve, or tell me what to change.
```
