# frontend-ui-modern — a shadcn/ui skill with a simulate → decide → build → mockup workflow

Install (any of these):

- **Project skill (Claude Code)**: copy this folder to `<repo>/.claude/skills/frontend-ui-modern/`.
- **Personal skill (Claude Code, all projects)**: copy it to `~/.claude/skills/frontend-ui-modern/`.
- **claude.ai**: save the `.skill` package (or the SKILL.md proposal card) from the chat.

Requirements: Node 18+ for the scripts; Playwright with Chromium for `scripts/mockup.mjs`
(`npm i -D playwright && npx playwright install chromium`; already present in Claude's cloud workspace).
Pair it with shadcn's own agent skill in the project: `npx skills add shadcn/ui`.

Quick tour:

```bash
node scripts/theme.mjs list                              # 20 dotfile themes + shadcn built-ins
node scripts/theme.mjs css catppuccin --extras           # :root + .dark for globals.css
node scripts/theme.mjs check nord                        # WCAG contrast report
node scripts/sim.mjs init ui-sim --project "My App"      # clickable simulation (edit the sim-screen sections)
node scripts/mockup.mjs http://localhost:3000 --widths 1440,390 --modes light,dark   # mockups + contact sheet
node scripts/theme.mjs build                             # regenerate references/themes/*.css and assets/themes.css after editing assets/palettes.json
open references/screenshots/gallery.html                 # 131 reference renders (themes, 14 views, states, overlays, styles, shells, mobile) — proof to diff builds against
node scripts/mockup.mjs ui-sim/index.html --views overview,customers --states normal,empty --overlays none,sheet --full   # sweep views/states/overlays
```

Everything the skill knows about shadcn/ui was verified on 2026-10-30 against `shadcn-ui/ui` main and ui.shadcn.com
(CLI 4.23, Base UI default, 8 styles, 28 blocks, 70 chart examples). Palettes are copied from each theme's official source
(see `assets/palettes.json`).
