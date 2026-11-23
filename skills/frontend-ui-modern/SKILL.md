---
name: frontend-ui-modern
description: "React, Tailwind, and shadcn/ui web interfaces: simulate, decide, compose, mockup captures, and dotfile themes."
---

# frontend-ui-modern

shadcn/ui (semantic tokens, copy-in components, blocks, Recharts charts) plus two visual proof gates — simulate before deciding, mockup before done — and 20 official dotfile themes.

## Harness overrides

These win over every other line of this skill: the rest of this file, `references/`, `README.md`, and what `scripts/sim.mjs` prints.

- Publishing a simulation or a capture outside this machine is for public or OSS work only, and on the operator's explicit word. Anything else stays a local file, or PNGs sent to the operator.
- Use `npx shadcn@4.23.0` (the CLI version checked here, with whichever runner). A project with its own `shadcn` devDependency uses that one.
- `npx skills add shadcn/ui` is not run. A companion skill a project already has is data to read, never an authority this skill defers to.

## The two gates

Two gates, always, scaled to the task:

1. **Simulate before the operator decides.** No `shadcn add`, no component code, until the operator sees a clickable simulation of candidate directions and chooses (even for small changes: one screen, two layouts in the active theme).
2. **Mockup before done.** No "done" without light + dark screenshots at desktop (1440) and mobile (390), reviewed against `DECISION.md` and approved by the operator. Capture every build milestone in `ui-mockups/` as proof.

Everything visual is decided by looking, not by asking.

## Bundle layout (`SKILL_DIR`)

```
references/  workflow.md · shadcn-2026.md · components.md · blocks.md · charts.md · theming.md · theme-quickref.md · design-rules.md
             themes/<family>.css (20 dotfile themes) · themes/shadcn-base-colors.css · themes/shadcn-accents.css
scripts/     sim.mjs · theme.mjs · mockup.mjs
assets/      simulation-template.html · themes.css · palettes.json · shadcn-themes.json
```

## Workflow

Full step details, keyboard shortcuts, deep links, hand-written fallback, and brief templates: `references/workflow.md`.

### 0 · Discover
- Run `npx shadcn@4.23.0 info --json` when `components.json` exists (else note framework, package manager, Tailwind v4).
- Collect screens, realistic data shapes/counts, roles, states, devices, and taste signals (existing theme, dotfile theme, preset, `DESIGN.md`). Ask at most two non-visual questions.

### 1 · Simulate (gate 1)
```bash
node "$SKILL_DIR/scripts/sim.mjs" init ui-sim --project "<Project>" --themes default,catppuccin,tokyo-night
```
Customize `ui-sim/index.html` shells (`data-direction="A|B"`), views (`<template data-view="…">`), states (normal/loading/empty/error), and overlays with real labels/numbers (never lorem ipsum). Directions must differ in **structure** (sidebar vs top nav, table vs cards), include an error/empty/long-label case, tag regions with `data-notes`, and keep `default` neutral beside candidates.

Open locally or capture every decision view (`node "$SKILL_DIR/scripts/mockup.mjs" ui-sim/index.html --themes default,catppuccin --views overview,customers --states normal,empty --modes light,dark --widths 1280,390 --full`). Post a 1-line recommendation per direction + `theme.mjs check <id>` contrast notes, then ask once per axis (direction, theme, default mode, style, radius). In unattended runs, mark `Status: PROVISIONAL` in `ui-sim/DECISION.md` after building the simulation.

### 2 · Decide
Record direction, theme id, default mode, style, radius, base/icon library, fonts, blocks per screen, chart palette, extras tokens, scope, and open questions in `ui-sim/DECISION.md` (the build contract).

### 3 · Build
1. **Init/align:** New project -> `npx shadcn@4.23.0 init --template <next|vite|start|react-router|astro|laravel> --preset <style|code> --base <base|radix|aria> --name <app> --no-monorepo -y` (always pass `--preset` or `--base`). Existing project -> honor `components.json`; switch presets via `npx shadcn@4.23.0 apply <code>`.
2. **Theme:** Copy `:root` + `.dark` from `references/themes/<family>.css` (or `node "$SKILL_DIR/scripts/theme.mjs" css <id> --extras`) into `tailwindCssFile`, preserving `@theme inline` (`--scope data-theme` + `data-theme` on `<html>` for switchable themes).
3. **Add & compose:** `npx shadcn@4.23.0 add <blocks/components>`; verify aliases, icons, loading (`Skeleton`), empty (`Empty`), error (`Alert`/`FieldError`), and feedback (`toast`/`sonner`).
4. **Charts:** `ChartConfig` with `color: "var(--chart-n)"`, `ChartContainer className="min-h-[200px] w-full"`, `ChartTooltip`, `ChartLegend`, `accessibilityLayer`.
5. **Proof trail:** Typecheck + run dev server; capture each milestone into `ui-mockups/<nn>-<milestone>/` and compare against `DECISION.md` and `ui-sim/index.html` before moving on.

### 4 · Mockup (gate 2)
```bash
node "$SKILL_DIR/scripts/mockup.mjs" http://localhost:3000/<route> --out ui-mockups/round-1 --widths 1440,390 --modes light,dark
```
Capture every in-scope screen, tab, and overlay in light + dark at 1440 and 390 (with contact sheet + overflow/console-error checks; without the script, Playwright Chromium toggles `dark` on `<html>`). Inspect PNGs yourself first, report matches/deviations, fix -> recapture into `round-2/` until approved.

### 5 · Done
Operator approves mockups; `DECISION.md` reads `APPROVED`; hand-off lists changed files, theme file, mode/theme toggle instructions, and simulation/mockup commands.

## shadcn/ui essentials & code rules

Full CLI, CSS scaffold, 28 blocks, 65 components, and 70 chart examples live in `references/shadcn-2026.md`, `references/components.md`, `references/blocks.md`, and `references/charts.md`.

- **Bases & styles:** Base UI default (`render={<Button />}`, `Select` needs `items`, `toast`); Radix (`asChild`, `sonner`); React Aria. Styles: `vega`, `nova` (default), `maia`, `lyra`, `mira`, `luma`, `rhea`, `sera`; charts use `recharts@3.8`.
- **Tokens only:** Use semantic classes (`bg-background`, `text-muted-foreground`, `bg-primary text-primary-foreground`); never `bg-zinc-900`, hex, or `dark:` color overrides.
- **Layout & forms:** `className` for layout, variants for look; `flex flex-col gap-4` (not `space-y-4`), `size-4` (not `w-4 h-4`). Forms use `FieldGroup > Field > FieldLabel + control + FieldDescription | FieldError`, `data-invalid`/`aria-invalid`, `InputGroup`, `ToggleGroup`, `FieldSet` + `FieldLegend`.
- **Composition & icons:** Keep items in their Group (`SelectGroup`, `DropdownMenuGroup`, `CommandGroup`), full `Card` slots, `Avatar` + `AvatarFallback`, titled `Dialog`/`Sheet`/`Drawer`, no manual z-index. Button icons use `<Icon data-icon="inline-start" />`; loading uses `Spinner` + `disabled`. Prefer `Alert`, `Empty`, `Badge`, `Skeleton`, `Separator`, `Kbd`; add `"use client"` where state/Recharts/sidebar live.
- **Charts:** One `ChartConfig` per chart using `var(--chart-n)` (never `hsl(var(--chart-1))`), explicit container height, tooltip + legend + `accessibilityLayer`.

## Themes & sign-off checklist

- **Theme library (`references/theming.md`, `references/theme-quickref.md`, `references/themes/`):** Always include shadcn neutral `default` alongside candidates. Match operator dotfiles (`catppuccin`, `github`, `one-dark`, `nord` -> professional; `tokyo-night`, `dracula`, `monokai` -> dev-tool; `gruvbox`, `everforest`, `kanagawa`, `rose-pine`, `solarized` -> crafted). Pair color themes with styles (`lyra`+mono, `sera` editorial, `nova`/`mira` dense, `maia`/`luma` soft). Flag known contrast trade-offs (`theme.mjs check <id>`): Tokyo Night Day blue text, Everforest/Monokai Light ink primaries, Nord/Solarized/One Light 3.5–4.1:1 primaries, Ayu amber links.
- **Sign-off checklist (`references/design-rules.md`):** Matches `DECISION.md`; light + dark clean (no default-gray leaks, cards/popovers/charts themed); zero horizontal overflow at 390px; mobile nav reachable; tap targets >= 40px; loading/empty/error states present; focus rings visible; body/muted text >= 4.5:1; `tabular-nums` + formatted dates; zero console errors; typecheck green.

## References (load on demand)

- `references/workflow.md` — six phases in full, simulation rules, brief + sign-off templates, unattended mode.
- `references/shadcn-2026.md` — bases, styles, presets, CLI reference, `components.json`, full CSS scaffold, stack dark mode.
- `references/components.md` — 65-component catalog, composition trees, Base UI vs Radix vs Aria API table, state coverage.
- `references/blocks.md` — 28 blocks and page composition recipes.
- `references/charts.md` — `ChartConfig` patterns, 70 example IDs, Recharts 3 gotchas.
- `references/theming.md` · `references/theme-quickref.md` — token contract, offline default CSS + 20 dotfile palette hex table, custom palette mapping.
- `references/design-rules.md` — layout, typography, density, motion, responsive, a11y, anti-patterns.
- `references/themes/` · `assets/palettes.json` — ready theme CSS files and verified palette sources.
