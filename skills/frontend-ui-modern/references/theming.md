# Theming: the token contract, shadcn's themes, and the dotfile theme library

Contents: [Token contract](#token-contract) · [shadcn's own themes](#shadcns-own-themes) · [Dotfile themes](#dotfile-themes) ·
[Applying a theme](#applying-a-theme) · [Mapping method](#mapping-method-for-any-palette) · [Theme notes](#per-theme-notes) ·
[Fonts & styles](#fonts-and-styles) · [Checks](#checks)

## Token contract

Every component reads these CSS variables (set in `:root` and `.dark`, bridged to Tailwind by `@theme inline`):

| Token | Controls | Used by |
| --- | --- | --- |
| `background` / `foreground` | page surface and default text | shell, sections, body |
| `card` / `card-foreground` | elevated surfaces | Card, panels, inset main |
| `popover` / `popover-foreground` | floating surfaces | Popover, menus, dialogs, combobox lists |
| `primary` / `primary-foreground` | high-emphasis actions, brand | default Button, selected states, badges |
| `secondary` / `secondary-foreground` | lower-emphasis fills | secondary Button/Badge |
| `muted` / `muted-foreground` | subtle surfaces, de-emphasized text | descriptions, placeholders, tabs list, skeleton |
| `accent` / `accent-foreground` | hover/active surfaces | ghost buttons, menu highlight, hovered rows |
| `destructive` | danger (2026 styles tint it: `bg-destructive/10 text-destructive`) | destructive Button/Badge, invalid states |
| `border` · `input` · `ring` | hairlines · control borders · focus rings | everything |
| `chart-1..5` | chart palette | `ChartConfig` via `var(--chart-n)` |
| `sidebar`, `-foreground`, `-primary(-foreground)`, `-accent(-foreground)`, `-border`, `-ring` | sidebar surface & states | Sidebar |
| `radius` | base radius; `--radius-sm…4xl` derive from it (×0.6, 0.8, 1, 1.4, 1.8, 2.2, 2.6) | all corners |

Optional extras this skill adds (emit with `theme.mjs … --extras`; register `--color-success` etc. in `@theme inline`):
`success` / `success-foreground`, `warning` / `warning-foreground`, `info` / `info-foreground` → `bg-success/10 text-success`, status badges, chart semantics.

Rules of the contract: dark mode = the same tokens under `.dark`; foreground always pairs with its surface; never hard-code colors in
components; `hsl()`/`oklch()`/hex all work as values (Tailwind v4 passes them through). shadcn's own tokens are OKLCH; the
dotfile themes keep the palettes' hex so values stay recognizable (`#cba6f7 /* mauve */`). Use `--format oklch` if a project insists on OKLCH.

## shadcn's own themes

- `references/themes/shadcn-base-colors.css` — complete `:root`/`.dark` sets for **neutral, stone, zinc, mauve, olive, mist, taupe**
  (what `init --base-color` / presets generate; note the grayscale `--chart-*` in generated presets vs the colorful docs set).
- `references/themes/shadcn-accents.css` — **amber blue cyan emerald fuchsia green indigo lime orange pink purple red rose sky teal violet yellow**:
  overrides for `primary`, `primary-foreground`, `secondary`, `secondary-foreground`, `chart-1..5`, `sidebar-primary(-foreground)`. Paste on top of a base color.
- The docs' default (neutral + colorful charts) is in `references/shadcn-2026.md#css-scaffold` and `assets/simulation-template.html`.
- Presets from https://ui.shadcn.com/create combine base color + accent + chart color + radius + fonts + menu style; apply with `npx shadcn@latest apply <code> --only theme`.

## Dotfile themes

Generated from verified official palettes (`assets/palettes.json` cites each source) into `references/themes/<family>.css`
(one `:root` + `.dark` pair per theme id, hex with palette names) and `assets/themes.css` (all ids, `[data-theme]`-scoped, for the simulator and multi-theme apps).
`node scripts/theme.mjs list` prints the ids. Ordered by how common they are in 2025–26 dotfiles/editor installs (Dotfyle, VS Code marketplace, GitHub stars):

| id | Light / Dark | Primary | Character | Watch out |
| --- | --- | --- | --- | --- |
| `catppuccin` | Latte / Mocha | mauve | soft pastels, warm, friendly; the current favorite | light muted text ≈ 4.4:1 → uses subtext1 |
| `catppuccin-macchiato`, `catppuccin-frappe` | Latte / Macchiato · Frappé | mauve | lower-contrast dark flavors | muted-on-muted < 4.5 |
| `tokyo-night` (+ `-storm`, `-moon`) | Day / Night · Storm · Moon | blue | deep navy, neon blue/purple; developer-tool feel | Day's text is a blue (#3760bf) — it is the theme; Day muted text 3.6:1 |
| `dracula` | Alucard / Dracula | purple | high-energy neon on purple-black; Alucard is the official 2025 light | comment gray too dim → muted text uses foreground @60% |
| `one-dark` | One Light / One Dark | blue | Atom's calm cool gray; universally familiar | One Light primary text 3.9:1 (white on #4078f2) |
| `github` | Light / Dark (Primer v11) | accent blue | "looks like GitHub"; great for dev tools | GitHub's own CTA is green — set `primary: success` if wanted |
| `gruvbox` | Light / Dark (medium) | orange | warm retro, strong contrast, cream paper light mode | muted-on-muted ~3.5 in light |
| `nord` | Snow Storm (derived) / Polar Night | frost cyan (dark) · nord10 (light) | arctic, calm, bluish grays | light primary text 3.5:1; light variant is derived from the official palette |
| `rose-pine` (+ `-moon`) | Dawn / Main · Moon | rose (dark) · pine (light) | moody, elegant, low-glare | Dawn uses pine as primary (rose fails contrast on cream); border contrast low |
| `kanagawa` (+ `-dragon`) | Lotus / Wave · Dragon | crystal blue · dragon blue | ink-wash, muted indigo & carp yellow; Lotus is a warm paper light | Lotus border contrast; Dragon is very desaturated |
| `solarized` | Light / Dark | blue | the 2011 classic; cyan-tinted | blue needs dark text (base03) to pass; muted ≈ 4.1 |
| `everforest` | Light / Dark (medium) | green (dark) · ink (light) | soft greens, very comfortable | light accents are too pale for button text → ink primary, green ring/charts |
| `ayu` (+ `-mirage`) | Light / Dark · Mirage | amber (dark text on it) | bright, clean; signature amber | amber on white fails as link color — use foreground for links in light |
| `monokai` | derived light / Monokai | green (dark) · ink (light) | 2006 Sublime classic; acid accents | no official light → neutral warm light with deepened accents, marked derived |

Which to suggest: match the operator's editor/terminal when they name one; for products, `catppuccin`, `github`, `one-dark`, `nord`
read as "professional"; `tokyo-night`, `dracula`, `monokai` read as "developer tool / hacker"; `gruvbox`, `everforest`, `kanagawa`,
`rose-pine`, `solarized` read as "crafted / warm". Always put the shadcn default next to the candidates in the simulation.

## Applying a theme

1. **Single theme (most apps)**: copy the `:root` + `.dark` pair for the id from `references/themes/<family>.css`
   (or `node scripts/theme.mjs css <id> --extras`) into `tailwindCssFile`, replacing the generated blocks. Keep `@theme inline`;
   add the extras mapping if used. Keep `--radius` from the decision.
2. **Switchable themes**: `node scripts/theme.mjs css <id> --scope data-theme --extras` per theme → import one CSS file; set
   `data-theme="<id>"` on `<html>` next to the `dark` class; persist both (next-themes `themes`/`value` or your own store).
3. **Preset code instead** (when the decision is a shadcn/create preset): `npx shadcn@latest apply <code>` or `init --preset <code> --force --no-reinstall`.
4. Sidebar: the inset/floating variants show `--sidebar` as the outer shell; the dotfile themes map it to the palette's dim tier
   (mantle, bg_dark, sumiInk1 …) so the main area floats lighter — check it in the simulation; use `--sidebar: var(--background)` for a flat look.
5. Verify in both modes with the mockup script; run `node scripts/theme.mjs check <id>` and report any body-text pair below 4.5:1.

## Mapping method (for any palette)

Roles → tokens (this is what `theme.mjs` does; use it for a brand palette or a DESIGN.md too):

| Role | Pick from the palette | → tokens |
| --- | --- | --- |
| `bg` | page background (light: the lightest paper; dark: the editor bg or one tier darker) | background |
| `surface` | one tier lighter than bg in dark, equal or lighter in light | card, popover |
| `surface2` | next tier (hover/selected/muted fills) | secondary, muted, accent, sidebar-accent |
| `border` | a tier above surface2, or `fg@0.1–0.18` | border, input, sidebar-border |
| `fg` / `fgMuted` | main text / comment-ish text that still passes 4.5:1 (`fg@0.6–0.75` when the palette's gray is too dim) | foreground + *-foreground, muted-foreground |
| `primary` / `primaryFg` | the palette's signature accent / the bg color that passes 4.5:1 on it | primary, ring, sidebar-primary |
| `destructive` | red | destructive |
| `chart1..5` | five distinct accents, ordered blue→purple→green→orange→pink-ish | chart-1..5 |
| `sidebar` | the dim tier (dark) / slightly tinted (light) | sidebar |
| `success` `warning` `info` (+Fg) | green / yellow / cyan-blue | extras |

Write them in a JSON file with the `assets/palettes.json` schema (`families.<name>.variants.<v>.{mode,palette,roles}` + `themes[]`) and run
`node scripts/theme.mjs css ./my-palette.json` / `check`. Keep hex as given; use `@alpha` for translucent borders/muted text rather than inventing colors.

## Per-theme notes

- **Catppuccin**: dark cards = `surface0`, hover = `surface1`, sidebar = `mantle`; the official style guide allows any accent as primary — swap `mauve`
  for `blue`/`teal`/`peach` by editing `roles.primary`. Latte muted text uses `subtext1` for AA.
- **Tokyo Night**: page = `bg_dark`, cards = `bg`, hover = `bg_highlight`, borders = `fg_gutter`; muted text = `fg@0.7`. Day pairs with every dark flavor.
- **Dracula**: surfaces from the official spec's UI tiers (`#343746` light, `#21222c` dark sidebar, `#191a21` darker); Alucard Classic is the official light (2025).
- **One Dark/Light**: Atom syntax values + One Dark UI tiers (sidebar `#21252b`); One Light border = `fg@0.15`.
- **GitHub**: Primer v11 functional tokens; `primary` = accent emphasis blue; `success` = green CTA color if you want GitHub's button style.
- **Gruvbox**: medium contrast; swap `bg0` for `bg0_h` (hard) / `bg0_s` (soft) in the palette for other contrasts; muted text = `fg3`.
- **Nord**: dark text = `nord6`, muted = `nord4@0.72`; light (derived) uses Snow Storm as surfaces and `nord10` as primary (3.5:1 — consider `nord3` ink primary for AA).
- **Rosé Pine**: Dawn text uses the 2025 palette value `#464261` (older ports: `#575279`); Dawn primary is `pine` for contrast.
- **Kanagawa**: Wave muted text = `fujiWhite@0.62`; Dragon is intentionally desaturated; Lotus borders are `lotusWhite0`.
- **Solarized**: two dark/light tones only, so `muted` uses `base1@0.12` / `base2`; primary text is `base03` on blue (4.1:1).
- **Everforest**: light accents fail text contrast → primary = `fg` ink, `ring` = green; dark primary = green.
- **Ayu**: v8.0.1 values (what every port uses); dark text on amber primary; light muted = `editorFg@0.9`.
- **Monokai**: VS Code's comment `#88846f` for muted text; light variant is derived (ink primary + deepened accents for charts).

## Fonts and styles

Theme files change colors only. Density, radius geometry and type come from the **style** (`nova`, `vega`, `maia`, `lyra`, `mira`, `luma`, `rhea`, `sera`)
and the **font** preset: match the dotfile vibe — `lyra` + JetBrains Mono/Geist Mono for terminal-like UIs, `sera` + Playfair for editorial,
`nova`/`rhea` for dense product UIs, `maia`/`luma` for consumer-soft. Set them at `init --preset <style>` or `apply <code> --only font`.
Radius: `none` (lyra/sera-like), `small` 0.45rem, `default` 0.625rem, `large` 0.875rem.

## Checks

`node scripts/theme.mjs check <id>` reports: foreground on background/card/sidebar, muted-foreground on background/muted, primary-foreground on primary (4.5:1),
primary/destructive on background (3:1), border visibility (1.3:1). Fix by moving a role one tier (e.g. `subtext0` → `subtext1`) or using `fg@alpha`;
accept decorative misses knowingly and tell the operator in the simulation brief.
