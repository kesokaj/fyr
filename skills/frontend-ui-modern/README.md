# frontend-ui-modern — a shadcn/ui skill with a simulate → decide → build → mockup workflow

Install (any of these):

- **Harness install**: keep this folder in `skills/frontend-ui-modern/` and run `fyr install` (Antigravity CLI loads skills from `~/.gemini/config/skills/` or project `.agents/skills/`).

Requirements: Node 18+ for the scripts; Playwright with Chromium for `scripts/mockup.mjs`
(`npm i -D playwright && npx playwright install chromium`).

Quick tour:

```bash
node scripts/theme.mjs list                              # 20 dotfile themes + shadcn built-ins
node scripts/theme.mjs css catppuccin --extras           # :root + .dark for globals.css
node scripts/theme.mjs check nord                        # WCAG contrast report
node scripts/sim.mjs init ui-sim --project "My App"      # clickable simulation (edit the sim-screen sections)
node scripts/mockup.mjs http://localhost:3000 --widths 1440,390 --modes light,dark   # mockups + contact sheet
node scripts/theme.mjs build                             # regenerate references/themes/*.css and assets/themes.css after editing assets/palettes.json
node scripts/mockup.mjs ui-sim/index.html --views overview,customers --states normal,empty --overlays none,sheet --full   # sweep views/states/overlays
```

Everything the skill knows about shadcn/ui was verified on 2026-10-30 against `shadcn-ui/ui` main and ui.shadcn.com
(CLI 4.23, Base UI default, 8 styles, 28 blocks, 70 chart examples). Palettes are copied from each theme's official source
(see `assets/palettes.json`).
