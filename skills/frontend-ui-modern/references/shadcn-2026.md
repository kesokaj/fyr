# shadcn/ui — current state (verified 2026-10-30 against shadcn-ui/ui `main` + ui.shadcn.com)

Contents: [Model](#model) · [CLI](#cli) · [components.json](#componentsjson) · [CSS scaffold](#css-scaffold) ·
[Dark mode](#dark-mode) · [Stacks](#stacks) · [Timeline](#timeline) · [Companion skill](#companion-skill)

## Model

- **Bases** (primitive library): `base` = Base UI (`@base-ui/react`, **default since 2026-07-02**), `radix`
  (unified `radix-ui` package), `aria` (`react-aria-components`, since 2026-07-17). Same component API across bases;
  only internals differ. Pick with `init --base`, in shadcn/create, or by the `style` prefix.
- **Styles** (geometry/density token sets, applied to component classes at install time):
  `vega` classic shadcn look · `nova` reduced padding (**default**) · `maia` rounded, generous spacing ·
  `lyra` boxy/sharp, mono fonts · `mira` dense/compact · `luma` soft, fluid, macOS-Tahoe-like (Mar 2026) ·
  `rhea` Luma but compact (May 2026) · `sera` editorial: serif headings, square corners, uppercase, underlined controls (Apr 2026).
  `components.json.style` is `<base>-<style>`, e.g. `base-nova`. Legacy frozen styles: `new-york`, `default` (Tailwind v3 era, docs at v3.shadcn.com).
- **Presets** pack style + baseColor + theme + chartColor + iconLibrary + font + fontHeading + radius + menuAccent + menuColor
  into a short code (`b5owWMfJ8l`). Build at https://ui.shadcn.com/create; inspect with `preset decode`; never decode by hand.
  Named presets = the 8 styles with their default fonts/icons: nova (Lucide/Geist), vega (Lucide/Inter), maia (Hugeicons/Figtree),
  lyra (Phosphor/JetBrains Mono), mira (Hugeicons/Inter), luma (Lucide/Inter), sera (Lucide/Noto Sans + Playfair Display, taupe), rhea (Lucide/Inter).
- **Base colors**: `neutral` (default), `stone`, `zinc`, `mauve`, `olive`, `mist`, `taupe` (gray/slate retired).
  **Accent themes** (override primary/secondary/chart/sidebar-primary only): amber blue cyan emerald fuchsia green indigo lime
  orange pink purple red rose sky teal violet yellow (+ the base colors themselves). **Chart color** = any theme name (defaults to theme).
  Ready CSS: `references/themes/shadcn-base-colors.css`, `references/themes/shadcn-accents.css`.
- **Radius**: `none` 0 · `small` 0.45rem · `default`/`medium` 0.625rem · `large` 0.875rem; scale derives from `--radius`
  (sm ×0.6, md ×0.8, lg ×1, xl ×1.4, 2xl ×1.8, 3xl ×2.2, 4xl ×2.6).
- **Icon libraries**: `lucide` (lucide-react), `tabler`, `hugeicons` (`<HugeiconsIcon icon={…}/>`), `phosphor`, `remixicon`. `migrate icons --from a --to b`.
- **Fonts** (registry items `font-<name>` / `font-heading-<name>`, Google provider): inter, geist, geist-mono, jetbrains-mono, figtree,
  noto-sans, nunito-sans, roboto, raleway, dm-sans, public-sans, outfit, oxanium, manrope, space-grotesk, montserrat, ibm-plex-sans,
  source-sans-3, instrument-sans, lora, merriweather, playfair-display, noto-serif, roboto-slab, eb-garamond, instrument-serif.
- **Menu accent** `subtle|bold` (bold: accent = primary) · **Menu color** `default|inverted|default-translucent|inverted-translucent`.
- **Registry**: items resolve from `https://ui.shadcn.com/r/styles/{style}/{name}.json`; namespaced registries `@acme/button`;
  GitHub registries `owner/repo/item` (private via `gh auth login`/`GH_TOKEN`); `registry:base` ships a whole design system;
  `registry:font` items; MCP server (`npx shadcn@latest mcp init --client claude`).
- **Packages**: `shadcn` (devDependency; provides `shadcn/tailwind.css` with `data-open:`/`data-closed:` variants, accordion
  keyframes, `no-scrollbar`, `scroll-fade`, `shimmer`), `cn` (replaces clsx + tailwind-merge; `lib/utils.ts` is `export { cn } from "cn"`),
  `class-variance-authority`, `tw-animate-css`, `@shadcn/react` (headless MessageScroller, Questionnaire), `@shadcn/helpers` (AI SDK / TanStack AI mock transports).
  Charts: `recharts@3.8.0`.

## CLI (shadcn 4.23)

```bash
npx shadcn@latest init [components...]   # or: create (alias). Interactive unless flags given.
  -t, --template <next|vite|start|react-router|laravel|astro>   -b, --base <base|radix|aria>
  -p, --preset [name|code|url]   -d, --defaults (= --template next --preset base-nova)   -n, --name <dir>
  --monorepo/--no-monorepo  --rtl/--no-rtl  --pointer/--no-pointer  --css-variables/--no-css-variables
  -f, --force  --reinstall/--no-reinstall  -y (default true)  -c <cwd>  -s
npx shadcn@latest add <names|@ns/item|owner/repo/item|url|./file.json> [-y] [-o] [-a] [-p path] [--dry-run] [--diff [file]] [--view [file]]
npx shadcn@latest apply <code> [--only theme,font]      # apply a preset to an existing project (keeps base + rtl)
npx shadcn@latest preset decode|resolve|info|url|open     # inspect/share preset codes
npx shadcn@latest view <items...>  |  search|list <@registry|owner/repo> [-q query] [-t ui,block,hook] [--json]
npx shadcn@latest docs <component...> [-b base] [--json]  # URLs for docs, examples, API
npx shadcn@latest info [--json]                            # framework, style, base, aliases, installed components, preset
npx shadcn@latest migrate <cn|icons|base-color|radix|rtl> [path] [--from a --to b] [-y]
npx shadcn@latest build [registry.json] [-o public/r]  |  registry validate|add|build  |  eject (inline shadcn/tailwind.css)
npx shadcn@latest mcp  |  mcp init --client <claude|cursor|vscode|codex|opencode>
```

Use the project's runner (`npx`, `pnpm dlx`, `bunx --bun`). Scaffolded templates (next, vite, start, react-router, astro;
`--monorepo` for all five; laravel has none) include dark mode for Next.js and Vite. Blocks land as pages
(`app/dashboard/page.tsx`, `app/login/page.tsx`; `pages/`, `app/routes/`, `resources/js/pages/` per framework) plus
components in `components/`; `registry:ui` → `components/ui/`, hooks → `hooks/`, lib → `lib/`.

Update flow: `add <c> --dry-run` → `add <c> --diff <file>` → merge by hand when local edits exist; `--overwrite` only
with explicit approval. Preset switch on an existing app: ask overwrite / partial (`apply --only theme,font`) / merge
(`init --preset <code> --force --no-reinstall` + per-component diff) / skip (config + CSS only).

## components.json

```json
{
  "$schema": "https://ui.shadcn.com/schema.json",
  "style": "base-nova",
  "rsc": true,
  "tsx": true,
  "tailwind": { "config": "", "css": "app/globals.css", "baseColor": "neutral", "cssVariables": true, "prefix": "" },
  "iconLibrary": "lucide",
  "aliases": { "components": "@/components", "utils": "@/lib/utils", "ui": "@/components/ui", "lib": "@/lib", "hooks": "@/hooks" },
  "registries": { "@acme": "https://registry.acme.com/{name}.json" }
}
```

`style`, `tailwind.baseColor`, `tailwind.cssVariables` are fixed after init (change via `apply`/`migrate base-color`/reinstall).
`tailwind.config` stays `""` on Tailwind v4. Aliases may be backed by `tsconfig paths` or `package.json#imports`
(`#components/*`, with `moduleResolution: "bundler"` + `resolvePackageJsonImports`). Optional: `rtl`, `menuColor`, `menuAccent`.

## CSS scaffold

What `init` writes to `tailwindCssFile` (Tailwind v4, neutral). The dotfile themes replace only the `:root`/`.dark` blocks.

```css
@import "tailwindcss";
@import "tw-animate-css";
@import "shadcn/tailwind.css";

@custom-variant dark (&:is(.dark *));

@theme inline {
  --color-background: var(--background);
  --color-foreground: var(--foreground);
  --color-card: var(--card);
  --color-card-foreground: var(--card-foreground);
  --color-popover: var(--popover);
  --color-popover-foreground: var(--popover-foreground);
  --color-primary: var(--primary);
  --color-primary-foreground: var(--primary-foreground);
  --color-secondary: var(--secondary);
  --color-secondary-foreground: var(--secondary-foreground);
  --color-muted: var(--muted);
  --color-muted-foreground: var(--muted-foreground);
  --color-accent: var(--accent);
  --color-accent-foreground: var(--accent-foreground);
  --color-destructive: var(--destructive);
  --color-border: var(--border);
  --color-input: var(--input);
  --color-ring: var(--ring);
  --color-chart-1: var(--chart-1);
  --color-chart-2: var(--chart-2);
  --color-chart-3: var(--chart-3);
  --color-chart-4: var(--chart-4);
  --color-chart-5: var(--chart-5);
  --color-sidebar: var(--sidebar);
  --color-sidebar-foreground: var(--sidebar-foreground);
  --color-sidebar-primary: var(--sidebar-primary);
  --color-sidebar-primary-foreground: var(--sidebar-primary-foreground);
  --color-sidebar-accent: var(--sidebar-accent);
  --color-sidebar-accent-foreground: var(--sidebar-accent-foreground);
  --color-sidebar-border: var(--sidebar-border);
  --color-sidebar-ring: var(--sidebar-ring);
  --radius-sm: calc(var(--radius) * 0.6);
  --radius-md: calc(var(--radius) * 0.8);
  --radius-lg: var(--radius);
  --radius-xl: calc(var(--radius) * 1.4);
  --radius-2xl: calc(var(--radius) * 1.8);
  --radius-3xl: calc(var(--radius) * 2.2);
  --radius-4xl: calc(var(--radius) * 2.6);
}

:root {
  --radius: 0.625rem;
  --background: oklch(1 0 0);
  --foreground: oklch(0.145 0 0);
  --card: oklch(1 0 0);
  --card-foreground: oklch(0.145 0 0);
  --popover: oklch(1 0 0);
  --popover-foreground: oklch(0.145 0 0);
  --primary: oklch(0.205 0 0);
  --primary-foreground: oklch(0.985 0 0);
  --secondary: oklch(0.97 0 0);
  --secondary-foreground: oklch(0.205 0 0);
  --muted: oklch(0.97 0 0);
  --muted-foreground: oklch(0.556 0 0);
  --accent: oklch(0.97 0 0);
  --accent-foreground: oklch(0.205 0 0);
  --destructive: oklch(0.577 0.245 27.325);
  --border: oklch(0.922 0 0);
  --input: oklch(0.922 0 0);
  --ring: oklch(0.708 0 0);
  --chart-1: oklch(0.646 0.222 41.116);
  --chart-2: oklch(0.6 0.118 184.704);
  --chart-3: oklch(0.398 0.07 227.392);
  --chart-4: oklch(0.828 0.189 84.429);
  --chart-5: oklch(0.769 0.188 70.08);
  --sidebar: oklch(0.985 0 0);
  --sidebar-foreground: oklch(0.145 0 0);
  --sidebar-primary: oklch(0.205 0 0);
  --sidebar-primary-foreground: oklch(0.985 0 0);
  --sidebar-accent: oklch(0.97 0 0);
  --sidebar-accent-foreground: oklch(0.205 0 0);
  --sidebar-border: oklch(0.922 0 0);
  --sidebar-ring: oklch(0.708 0 0);
}

.dark {
  --background: oklch(0.145 0 0);
  --foreground: oklch(0.985 0 0);
  --card: oklch(0.205 0 0);
  --card-foreground: oklch(0.985 0 0);
  --popover: oklch(0.205 0 0);
  --popover-foreground: oklch(0.985 0 0);
  --primary: oklch(0.922 0 0);
  --primary-foreground: oklch(0.205 0 0);
  --secondary: oklch(0.269 0 0);
  --secondary-foreground: oklch(0.985 0 0);
  --muted: oklch(0.269 0 0);
  --muted-foreground: oklch(0.708 0 0);
  --accent: oklch(0.269 0 0);
  --accent-foreground: oklch(0.985 0 0);
  --destructive: oklch(0.704 0.191 22.216);
  --border: oklch(1 0 0 / 10%);
  --input: oklch(1 0 0 / 15%);
  --ring: oklch(0.556 0 0);
  --chart-1: oklch(0.488 0.243 264.376);
  --chart-2: oklch(0.696 0.17 162.48);
  --chart-3: oklch(0.769 0.188 70.08);
  --chart-4: oklch(0.627 0.265 303.9);
  --chart-5: oklch(0.645 0.246 16.439);
  --sidebar: oklch(0.205 0 0);
  --sidebar-foreground: oklch(0.985 0 0);
  --sidebar-primary: oklch(0.488 0.243 264.376);
  --sidebar-primary-foreground: oklch(0.985 0 0);
  --sidebar-accent: oklch(0.269 0 0);
  --sidebar-accent-foreground: oklch(0.985 0 0);
  --sidebar-border: oklch(1 0 0 / 10%);
  --sidebar-ring: oklch(0.556 0 0);
}

@layer base {
  * { @apply border-border outline-ring/50; }
  body { @apply bg-background text-foreground; }
}
```

Notes: presets generated by shadcn/create emit a **grayscale** chart palette for `neutral` (chart color is its own
preset field); the docs page shows the colorful set above. Fonts from presets add `--font-sans`/`--font-heading`/`--font-mono`
to `@theme inline`. Adding a token = define in `:root` + `.dark`, register `--color-<name>` in `@theme inline`.
`init --no-css-variables` generates utility-class components instead (installation-time choice).

## Dark mode

All setups toggle the `dark` class on `<html>` (because of `@custom-variant dark (&:is(.dark *))`).

**Next.js** — `npm install next-themes`; `components/theme-provider.tsx`:
```tsx
"use client"
import * as React from "react"
import { ThemeProvider as NextThemesProvider } from "next-themes"
export function ThemeProvider({ children, ...props }: React.ComponentProps<typeof NextThemesProvider>) {
  return <NextThemesProvider {...props}>{children}</NextThemesProvider>
}
```
In `app/layout.tsx`: `<html lang="en" suppressHydrationWarning>` and wrap `{children}` in
`<ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>`. Toggle with
`useTheme().setTheme("light"|"dark"|"system")` in a DropdownMenu (Base UI: `<DropdownMenuTrigger render={<Button variant="outline" size="icon" />}>`).

**Vite** — custom provider (from the docs): context with `theme` in `localStorage("vite-ui-theme")`, effect that removes
`light`/`dark` from `document.documentElement` and adds the resolved one (system via `matchMedia("(prefers-color-scheme: dark)")`).
Wrap `<App>` in `<ThemeProvider defaultTheme="system" storageKey="vite-ui-theme">`.

**Astro**: inline `<script is:inline>` reading localStorage/`matchMedia` and toggling `.dark` before paint.
**Remix / React Router**: `remix-themes` with a cookie session; add `:root[class~="dark"]`. **TanStack Start**: provider that
sets the class pre-hydration with `ScriptOnce`.

Multi-theme (user picks Catppuccin vs Nord at runtime): set `data-theme="<id>"` on `<html>` alongside the `dark` class and
use `[data-theme]`-scoped CSS (`node scripts/theme.mjs css <id> --scope data-theme`). Persist both in the same store.

## Stacks

| Stack | Scaffold | Alias | Notes |
| --- | --- | --- | --- |
| Next.js (App Router) | `npx shadcn@latest init -t next` or `create-next-app` + `init` | `@/` | `rsc: true`; CLI adds `"use client"`; charts/sidebar are client components |
| Vite + React | `init -t vite` or `npm create vite` + `@tailwindcss/vite` + paths in `tsconfig.json` **and** `tsconfig.app.json` | `@/` | `rsc: false` |
| TanStack Start | `init -t start` (or `@tanstack/cli create` without the shadcn add-on) | `@/` | routes in `src/routes` |
| React Router v7 | `init -t react-router` (`npm create react-router` has Tailwind + `~/*`) | `~/` | Remix page kept separately |
| Astro | `init -t astro` (`--template with-tailwindcss --add react`) | `@/` | React islands need `client:*` directives |
| Laravel (Inertia React) | `laravel new` + React starter, then `init` from app root | `@/` | files in `resources/js/components/ui` |
| Monorepo | `init --monorepo` → `apps/web` + `packages/ui`; `add -c apps/web`; import `@workspace/ui/components/...` | | run `add` in the app workspace |

## Timeline (what changed recently — useful when older snippets look different)

2025-02 Tailwind v4 + React 19 (no forwardRef, `data-slot`, OKLCH, `tw-animate-css`, `toast`→`sonner`, `new-york` default) ·
2025-06 new Calendar · 2025-08 CLI 3 + MCP + namespaced registries · 2025-10 Spinner, Kbd, ButtonGroup, InputGroup, Field, Item, Empty ·
2025-12 `shadcn create`: styles Vega/Nova/Maia/Lyra/Mira, Radix or Base UI, fonts/icons/colors per project ·
2026-01 Base UI docs, `inline-start/end` sides, RTL · 2026-02 unified `radix-ui` import, all blocks for both bases (OTP + calendar blocks removed) ·
2026-03 **CLI v4**: skills, `--preset` codes, `--dry-run/--diff/--view`, `init --template/--base`, `info`, `docs`, fonts as registry items; Luma ·
2026-04 composition trees in docs, `apply`, Sera, `--only theme,font`, `--pointer`, `preset decode|resolve|url|open` ·
2026-05 `package.json#imports` aliases, registry `include`, Rhea, `eject` · 2026-06 GitHub registries; chat components (MessageScroller, Message, Bubble, Attachment, Marker), `scroll-fade`, `shimmer` ·
2026-07 **Base UI default**; Typeset; `@shadcn/helpers`; React Aria base; Toast (Base UI); dynamic registry search ·
2026-08 Questionnaire; helpers human-in-the-loop; private GitHub registries · 2026-09 `cn` package (`migrate cn`) ·
2026-10 Trace, Code Block, **AI Elements** (21 agentic-UI patterns, replaces AI SDK Elements; pages available as `.md`).

## Companion skill

`npx skills add shadcn/ui` installs shadcn's own agent skill into a project (it injects `shadcn info --json`, enforces
composition rules, documents the CLI and registries, and includes a Radix→Base migration skill). When it is present,
defer to it for CLI mechanics and component rules; this skill adds the decision workflow, the theme library, the
simulation and mockup gates, and the block/chart recipes.
