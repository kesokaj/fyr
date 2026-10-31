#!/usr/bin/env node
/**
 * sim.mjs — scaffold a UI simulation (single HTML file, zero deps) from assets/simulation-template.html,
 * with every theme from assets/themes.css inlined and a theme list the operator can switch between.
 *
 *   node scripts/sim.mjs init ui-sim --project "Acme Admin"                 # all themes
 *   node scripts/sim.mjs init ui-sim --project "Acme Admin" --themes default,catppuccin,nord,tokyo-night
 *   node scripts/sim.mjs init ui-sim --custom ./my-palette.json             # include a custom palette (see theme.mjs)
 *
 * Then edit ui-sim/index.html: replace the <section class="sim-screen"> blocks with the project's real
 * screens (2–3 directions), keep realistic content, and open it (or publish it as an artifact) for the
 * operator. `node scripts/mockup.mjs ui-sim/index.html --themes nord,catppuccin --modes light,dark`
 * captures PNGs of the simulation if the operator can't open HTML.
 */
import fs from "node:fs"
import path from "node:path"
import { execFileSync } from "node:child_process"
import { fileURLToPath } from "node:url"

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.resolve(__dirname, "..")

function parseArgs(argv) {
  const args = { _: [] }
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    if (a.startsWith("--")) { const n = argv[i + 1]; if (n && !n.startsWith("--")) { args[a.slice(2)] = n; i++ } else args[a.slice(2)] = true }
    else args._.push(a)
  }
  return args
}

const args = parseArgs(process.argv.slice(2))
const cmd = args._[0]
if (cmd !== "init") {
  console.log(fs.readFileSync(fileURLToPath(import.meta.url), "utf8").split("*/")[0].replace(/^\/\*\*?/, ""))
  process.exit(cmd ? 1 : 0)
}

const outDir = path.resolve(args._[1] || "ui-sim")
const project = args.project || path.basename(process.cwd())
const themesCssPath = path.join(ROOT, "assets", "themes.css")
if (!fs.existsSync(themesCssPath)) {
  console.log("assets/themes.css missing — building it with theme.mjs …")
  execFileSync(process.execPath, [path.join(ROOT, "scripts", "theme.mjs"), "build"], { stdio: "inherit" })
}
let themesCss = fs.readFileSync(themesCssPath, "utf8")
const palettes = JSON.parse(fs.readFileSync(path.join(ROOT, "assets", "palettes.json"), "utf8"))
let list = [{ id: "default", title: "shadcn default (neutral)" }, ...palettes.themes.map((t) => ({ id: t.id, title: t.title }))]

if (args.custom) {
  const custom = JSON.parse(fs.readFileSync(path.resolve(args.custom), "utf8"))
  for (const t of custom.themes || []) {
    const css = execFileSync(process.execPath, [path.join(ROOT, "scripts", "theme.mjs"), "css", path.resolve(args.custom), "--scope", "data-theme", "--extras"], { encoding: "utf8" })
    themesCss += "\n" + css
    list.push({ id: t.id, title: t.title + " (custom)" })
  }
}
if (args.themes && args.themes !== "all") {
  const wanted = String(args.themes).split(",").map((s) => s.trim())
  const missing = wanted.filter((w) => !list.some((t) => t.id === w))
  if (missing.length) { console.error(`Unknown theme id(s): ${missing.join(", ")}. Run: node scripts/theme.mjs list`); process.exit(1) }
  list = wanted.map((w) => list.find((t) => t.id === w))
}

let html = fs.readFileSync(path.join(ROOT, "assets", "simulation-template.html"), "utf8")
html = html.replace("/*__THEMES__*/", themesCss)
html = html.replace(/\/\*__THEME_LIST__\*\/\[[^\n]*\];/, `${JSON.stringify(list)};`)
html = html.replaceAll("__PROJECT__", project.replace(/[<>&]/g, ""))

fs.mkdirSync(outDir, { recursive: true })
fs.writeFileSync(path.join(outDir, "index.html"), html)
if (!fs.existsSync(path.join(outDir, "DECISION.md"))) {
  fs.writeFileSync(path.join(outDir, "DECISION.md"), `# UI decision — ${project}

Status: PENDING (fill in after the operator has reviewed ui-sim/index.html)

| Choice | Value | Why |
| --- | --- | --- |
| Direction | A / B / C — <title> | |
| Theme | <id> (light: …, dark: …) | |
| Default mode | light / dark / system | |
| Style (shadcn/create) | nova / vega / maia / lyra / mira / luma / rhea / sera | |
| Radius | none / small / default / large | |
| Base library | base (default) / radix / aria | |
| Icon library | lucide / tabler / hugeicons / phosphor / remixicon | |
| Fonts | body: …, heading: … | |
| Layout blocks | e.g. sidebar-07 + dashboard-01, login-04 | |
| Chart palette | --chart-1..5 from theme / accent theme | |
| Extras | success / warning / info tokens: yes / no | |

Screens in scope: …
Out of scope for this round: …
Open questions: …
`)
}
console.log(`Simulation written to ${path.relative(process.cwd(), path.join(outDir, "index.html"))} with ${list.length} theme(s): ${list.map((t) => t.id).join(", ")}`)
console.log(`Next: edit the <section class="sim-screen"> blocks with the real screens, then show it to the operator (open the file, or publish it as an HTML artifact).`)
