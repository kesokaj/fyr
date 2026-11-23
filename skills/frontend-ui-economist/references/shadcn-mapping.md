# Mapping to frontend-ui-modern (shadcn/ui)

Reference for `skills/frontend-ui-economist/SKILL.md`.

## Choosing between the two UI skills

| | `frontend-ui-economist` | `frontend-ui-modern` |
|---|---|---|
| Stack | plain HTML/CSS/SVG: artifacts, static pages, prototypes, server-rendered templates | shadcn/ui on Tailwind + React (Next.js, Vite, TanStack) |
| Look | editorial (Economist-inspired), Google-airy, dense console, dark-first | shadcn styles + 20 dotfile themes (Catppuccin, Tokyo Night, Dracula, Nord) |
| Charts | `assets/charts.js`, editorial and tool styles | Recharts through shadcn `ChartContainer` |
| Method | token contract, white-space measurement, anti-pattern checks | simulate -> decide -> build with CLI -> mockup gates |

Hand off to `frontend-ui-modern` whenever the project uses shadcn, Tailwind, React components, dotfile themes, or `components.json`.

## Token port table (into shadcn `:root` / `.dark`)

| Economist token | shadcn token |
|---|---|
| `--bg` | `--background` |
| `--ink` | `--foreground`, `--card-foreground`, `--popover-foreground` |
| `--surface` | `--card`, `--popover` |
| `--surface-2` | `--secondary`, `--muted`, `--accent`, `--sidebar-accent` |
| `--ink-2` | `--muted-foreground`, `--secondary-foreground` |
| `--line` | `--border`, `--input`, `--sidebar-border` |
| `--accent` / `--accent-ink` | `--primary`, `--ring`, `--sidebar-primary` / `--primary-foreground` |
| `--bad` | `--destructive` |
| `--s1 ... --s4` | `--chart-1 ... --chart-4` (validate a 5th with `scripts/check_theme.js`) |
| `--side-bg` / `--side-ink` | `--sidebar` / `--sidebar-foreground` |
| `--radius` | `--radius` (Broadsheet `0px` -> `none`; Airy `12px` -> `large`) |

Note: `--panel-top`, `--nav-mark`, and `--chart-tab` have no built-in shadcn equivalent; add those three manually when porting editorial themes so the result does not read as plain shadcn with a serif font.
