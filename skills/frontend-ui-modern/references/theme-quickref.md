# Theme Quick Reference (Offline Baseline & Dotfile Palettes)

Reference for `skills/frontend-ui-modern/SKILL.md`. Full token contract, per-theme notes, and contrast rules live in `references/theming.md`; ready CSS files live in `references/themes/<family>.css`, `references/themes/shadcn-base-colors.css`, and `references/themes/shadcn-accents.css`.

## shadcn default (neutral) baseline

```css
:root { --radius: 0.625rem; --background: oklch(1 0 0); --foreground: oklch(0.145 0 0); --card: oklch(1 0 0); --card-foreground: oklch(0.145 0 0);
  --popover: oklch(1 0 0); --popover-foreground: oklch(0.145 0 0); --primary: oklch(0.205 0 0); --primary-foreground: oklch(0.985 0 0);
  --secondary: oklch(0.97 0 0); --secondary-foreground: oklch(0.205 0 0); --muted: oklch(0.97 0 0); --muted-foreground: oklch(0.556 0 0);
  --accent: oklch(0.97 0 0); --accent-foreground: oklch(0.205 0 0); --destructive: oklch(0.577 0.245 27.325); --border: oklch(0.922 0 0);
  --input: oklch(0.922 0 0); --ring: oklch(0.708 0 0); --chart-1: oklch(0.646 0.222 41.116); --chart-2: oklch(0.6 0.118 184.704);
  --chart-3: oklch(0.398 0.07 227.392); --chart-4: oklch(0.828 0.189 84.429); --chart-5: oklch(0.769 0.188 70.08); --sidebar: oklch(0.985 0 0);
  --sidebar-foreground: oklch(0.145 0 0); --sidebar-primary: oklch(0.205 0 0); --sidebar-primary-foreground: oklch(0.985 0 0);
  --sidebar-accent: oklch(0.97 0 0); --sidebar-accent-foreground: oklch(0.205 0 0); --sidebar-border: oklch(0.922 0 0); --sidebar-ring: oklch(0.708 0 0); }
.dark { --background: oklch(0.145 0 0); --foreground: oklch(0.985 0 0); --card: oklch(0.205 0 0); --card-foreground: oklch(0.985 0 0);
  --popover: oklch(0.205 0 0); --popover-foreground: oklch(0.985 0 0); --primary: oklch(0.922 0 0); --primary-foreground: oklch(0.205 0 0);
  --secondary: oklch(0.269 0 0); --secondary-foreground: oklch(0.985 0 0); --muted: oklch(0.269 0 0); --muted-foreground: oklch(0.708 0 0);
  --accent: oklch(0.269 0 0); --accent-foreground: oklch(0.985 0 0); --destructive: oklch(0.704 0.191 22.216); --border: oklch(1 0 0 / 10%);
  --input: oklch(1 0 0 / 15%); --ring: oklch(0.556 0 0); --chart-1: oklch(0.488 0.243 264.376); --chart-2: oklch(0.696 0.17 162.48);
  --chart-3: oklch(0.769 0.188 70.08); --chart-4: oklch(0.627 0.265 303.9); --chart-5: oklch(0.645 0.246 16.439); --sidebar: oklch(0.205 0 0);
  --sidebar-foreground: oklch(0.985 0 0); --sidebar-primary: oklch(0.488 0.243 264.376); --sidebar-primary-foreground: oklch(0.985 0 0);
  --sidebar-accent: oklch(0.269 0 0); --sidebar-accent-foreground: oklch(0.985 0 0); --sidebar-border: oklch(1 0 0 / 10%); --sidebar-ring: oklch(0.556 0 0); }
```

## Dotfile themes (20 ids, `bg · fg · card · primary`)

| id | Light / Dark | primary | light: bg · fg · card · primary | dark: bg · fg · card · primary |
| --- | --- | --- | --- | --- |
| `catppuccin` | Latte / Mocha | mauve | #eff1f5 · #4c4f69 · #eff1f5 · #8839ef | #1e1e2e · #cdd6f4 · #313244 · #cba6f7 |
| `catppuccin-macchiato` · `-frappe` | Latte / Macchiato · Frappé | mauve | (Latte) | #24273a · #cad3f5 · #363a4f · #c6a0f6 · · · #303446 · #c6d0f5 · #414559 · #ca9ee6 |
| `tokyo-night` · `-storm` · `-moon` | Day / Night · Storm · Moon | blue | #e1e2e7 · #3760bf · #e1e2e7 · #2e7de9 | #16161e · #c0caf5 · #1a1b26 · #7aa2f7 (storm #1f2335/#24283b; moon #1e2030/#222436 · #82aaff) |
| `dracula` | Alucard / Dracula | purple | #fffbeb · #1f1f1f · #fffbeb · #644ac9 | #282a36 · #f8f8f2 · #343746 · #bd93f9 |
| `one-dark` | One Light / One Dark | blue | #fafafa · #383a42 · #fafafa · #4078f2 | #282c34 · #abb2bf · #2c323c · #61afef |
| `github` | Light / Dark (Primer v11) | accent blue | #ffffff · #1f2328 · #ffffff · #0969da | #0d1117 · #f0f6fc · #151b23 · #1f6feb |
| `gruvbox` | Light / Dark (medium) | orange | #fbf1c7 · #3c3836 · #f9f5d7 · #af3a03 | #282828 · #ebdbb2 · #3c3836 · #fe8019 |
| `nord` | Snow Storm (derived) / Polar Night | frost | #eceff4 · #2e3440 · #eceff4 · #5e81ac | #2e3440 · #eceff4 · #3b4252 · #88c0d0 |
| `rose-pine` · `-moon` | Dawn / Main · Moon | rose (pine in Dawn) | #faf4ed · #464261 · #fffaf3 · #286983 | #191724 · #e0def4 · #1f1d2e · #ebbcba (moon #232136 · #2a273f · #ea9a97) |
| `kanagawa` · `-dragon` | Lotus / Wave · Dragon | crystal blue | #f2ecbc · #545464 · #f2ecbc · #4d699b | #1f1f28 · #dcd7ba · #2a2a37 · #7e9cd8 (dragon #181616 · #c5c9c5 · #282727 · #8ba4b0) |
| `solarized` | Light / Dark | blue | #fdf6e3 · #586e75 · #fdf6e3 · #268bd2 | #002b36 · #93a1a1 · #073642 · #268bd2 |
| `everforest` | Light / Dark (medium) | green (ink in light) | #fdf6e3 · #5c6a72 · #fdf6e3 · #5c6a72 | #2d353b · #d3c6aa · #343f44 · #a7c080 |
| `ayu` · `-mirage` | Light / Dark · Mirage | amber | #fcfcfc · #5c6166 · #fcfcfc · #ffaa33 | #0d1017 · #bfbdb6 · #0f131a · #e6b450 (mirage #242936 · #cccac2 · #ffcc66) |
| `monokai` | derived light / Monokai | green (ink in light) | #fdfcf8 · #272822 · #fdfcf8 · #272822 | #272822 · #f8f8f2 · #34352f · #a6e22e |

Mapping rule (`theme.mjs`): `bg -> background`, `surface -> card, popover`, `surface2 -> secondary, muted, accent, sidebar-accent`, `border -> border, input, sidebar-border`, `fg -> foreground + *-foreground`, `fgMuted -> muted-foreground`, `primary -> primary, ring, sidebar-primary`, `primaryFg -> primary-foreground`, `destructive`, `chart1..5`, `sidebar`, `success/warning/info(+Fg)` extras. Run `node "$SKILL_DIR/scripts/theme.mjs" css ./my-palette.json` / `check`.
