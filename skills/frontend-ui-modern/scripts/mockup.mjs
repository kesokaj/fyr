#!/usr/bin/env node
/**
 * mockup.mjs — capture mockup screenshots of a running app (or an HTML file) in light + dark,
 * desktop + mobile, and assemble a contact sheet the operator can review before sign-off.
 * Needs Playwright with Chromium (`npm i -D playwright && npx playwright install chromium`
 * in a project; already available in Claude's cloud workspace).
 *
 *   node scripts/mockup.mjs http://localhost:3000/dashboard                    # light+dark × 1440,390 → ui-mockups/
 *   node scripts/mockup.mjs http://localhost:3000/dashboard --out ui-mockups/round-2 --widths 1440,1024,390
 *   node scripts/mockup.mjs ui-sim/index.html --themes default,nord,catppuccin --modes light,dark   # simulation sweep
 *   node scripts/mockup.mjs ui-sim/index.html --views overview,customers,settings-general --states normal,empty --overlays none,command
 *   node scripts/mockup.mjs http://localhost:5173 --dark class --wait "[data-slot=sidebar]" --full
 *
 * Options
 *   --out <dir>          output directory (default ui-mockups)
 *   --widths <list>      viewport widths (default 1440,390); heights are 900 for ≥1024 and 844 below
 *   --modes <list>       light,dark (default both)
 *   --dark <strategy>    how to force dark mode: class (add .dark to <html>, default) | media (prefers-color-scheme)
 *                        | attr=<name>:<value> (e.g. attr=data-theme:dark) | query (append ?dark=1, for the simulation)
 *   --themes <list>      for the simulation only: theme ids to sweep (appends ?theme=<id>)
 *   --views <list>       simulation views to sweep (appends ?view=<id>; see the view select in the toolbar)
 *   --states <list>      simulation states to sweep: normal,loading,empty,error (appends ?state=)
 *   --overlays <list>    simulation overlays to sweep: none,command,dialog,confirm,sheet,toast,mobile-nav (appends ?overlay=)
 *   --query <string>     extra query string appended to every URL (e.g. "dir=B&style=luma")
 *   --wait <selector>    wait for this selector before capturing (after network idle)
 *   --delay <ms>         extra settle time (default 400)
 *   --full               full-page capture (default: viewport height, which keeps sheets readable)
 *   --name <prefix>      file name prefix (default mockup)
 */
import fs from "node:fs"
import path from "node:path"
import { pathToFileURL } from "node:url"

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
const target = args._[0]
if (!target) { console.log(fs.readFileSync(new URL(import.meta.url), "utf8").split("*/")[0].replace(/^\/\*\*?/, "")); process.exit(1) }

// Resolve Playwright from the project, then from global/NODE_PATH installs (ESM import ignores NODE_PATH, require() honors it).
let playwright
try { playwright = await import("playwright") } catch {
  try { const { createRequire } = await import("node:module"); playwright = createRequire(path.join(process.cwd(), "noop.js"))("playwright") } catch {
    console.error("Playwright not found. Install: npm i -D playwright && npx playwright install chromium"); process.exit(1)
  }
}

const out = path.resolve(args.out || "ui-mockups")
const widths = String(args.widths || "1440,390").split(",").map(Number)
const modes = String(args.modes || "light,dark").split(",")
const themes = args.themes ? String(args.themes).split(",") : [null]
const views = args.views ? String(args.views).split(",") : [null]
const states = args.states ? String(args.states).split(",") : [null]
const overlays = args.overlays ? String(args.overlays).split(",") : [null]
const darkStrategy = args.dark ? String(args.dark) : (/\.html?$/i.test(target) ? "query" : "class")
const prefix = args.name || "mockup"
const delay = Number(args.delay || 400)
fs.mkdirSync(out, { recursive: true })

const isFile = !/^https?:\/\//i.test(target)
const baseUrl = isFile ? pathToFileURL(path.resolve(target)).href : target
function buildUrl({ theme, mode, width, view, state, overlay }) {
  const u = new URL(baseUrl)
  if (args.query) for (const [k, v] of new URLSearchParams(String(args.query))) u.searchParams.set(k, v)
  if (theme) u.searchParams.set("theme", theme)
  if (view) u.searchParams.set("view", view)
  if (state) u.searchParams.set("state", state)
  if (overlay) u.searchParams.set("overlay", overlay)
  if (darkStrategy === "query") { u.searchParams.set("dark", mode === "dark" ? "1" : "0"); if (width) u.searchParams.set("vp", String(width)) }
  return u.href
}

const browser = await playwright.chromium.launch()
const shots = []
try {
  for (const theme of themes) for (const view of views) for (const state of states) for (const overlay of overlays) for (const mode of modes) for (const width of widths) {
    const height = width >= 1024 ? 900 : 844
    const context = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: 1, colorScheme: darkStrategy === "media" ? mode : "light" })
    const page = await context.newPage()
    const errors = []
    page.on("pageerror", (e) => errors.push(String(e)))
    page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()) })
    // Force the mode before any script runs so there is no flash and theme providers (next-themes) agree.
    if (darkStrategy === "class") await context.addInitScript((isDark) => { try { localStorage.setItem("theme", isDark ? "dark" : "light"); localStorage.setItem("vite-ui-theme", isDark ? "dark" : "light") } catch {} document.addEventListener("DOMContentLoaded", () => document.documentElement.classList.toggle("dark", isDark)) }, mode === "dark")
    if (darkStrategy.startsWith("attr=")) { const [n, v] = darkStrategy.slice(5).split(":"); await context.addInitScript(([n, v, isDark]) => document.addEventListener("DOMContentLoaded", () => { if (isDark) document.documentElement.setAttribute(n, v); else document.documentElement.removeAttribute(n) }), [n, v, mode === "dark"]) }
    const url = buildUrl({ theme, mode, width, view, state, overlay })
    await page.goto(url, { waitUntil: "networkidle", timeout: 60000 })
    if (darkStrategy === "class") await page.evaluate((isDark) => { document.documentElement.classList.toggle("dark", isDark); document.documentElement.style.colorScheme = isDark ? "dark" : "light" }, mode === "dark")
    if (args.wait) await page.waitForSelector(String(args.wait), { timeout: 15000 })
    await page.evaluate(() => document.fonts?.ready)
    await page.waitForTimeout(delay)
    const overflow = await page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, innerWidth: window.innerWidth, scrollY: window.scrollY }))
    const tag = [theme, view, state && state !== "normal" ? state : null, overlay && overlay !== "none" ? overlay : null].filter(Boolean).join("-")
    const file = `${prefix}${tag ? "-" + tag : ""}-${mode}-${width}.png`
    await page.screenshot({ path: path.join(out, file), fullPage: !!args.full })
    shots.push({ file, theme, view, state, overlay, mode, width, height, errors, overflow })
    console.log(`${file}${overflow.scrollWidth > overflow.innerWidth ? "  ⚠ horizontal overflow" : ""}${errors.length ? `  ⚠ ${errors.length} console error(s)` : ""}`)
    await context.close()
  }
} finally { await browser.close() }

// Contact sheet
const groups = {}
for (const s of shots) (groups[[s.theme, s.view].filter(Boolean).join(" · ") || "app"] ??= []).push(s)
const esc = (s) => String(s).replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]))
const html = `<!doctype html><meta charset="utf-8"><title>Mockup review — ${esc(target)}</title>
<style>body{margin:0;background:#0f0f10;color:#d4d4d8;font:13px ui-monospace,Menlo,monospace}header{padding:12px 16px;border-bottom:1px solid #27272a;background:#18181b;position:sticky;top:0}h2{font-size:13px;margin:24px 16px 8px;color:#fafafa}.row{display:flex;gap:16px;padding:0 16px 8px;overflow-x:auto}.shot{flex:none}.shot img{display:block;max-height:70vh;border-radius:8px;box-shadow:0 0 0 1px #3f3f46}.cap{padding:6px 2px;color:#a1a1aa}.warn{color:#fbbf24}
.checklist{margin:24px 16px;padding:16px;border:1px solid #27272a;border-radius:8px;max-width:720px}.checklist li{margin:4px 0}</style>
<header><b>MOCKUP REVIEW</b> · ${esc(target)} · ${new Date().toISOString().slice(0, 16).replace("T", " ")} · ${shots.length} capture(s)</header>
${Object.entries(groups).map(([g, list]) => `<h2>${esc(g)}</h2>` + modes.map((m) => `<div class="row">${list.filter((s) => s.mode === m).map((s) => `<div class="shot"><img src="${s.file}" alt="${esc(s.file)}"><div class="cap">${[s.state && s.state !== "normal" ? s.state : null, s.overlay && s.overlay !== "none" ? "overlay " + s.overlay : null].filter(Boolean).join(" · ")}${(s.state && s.state !== "normal") || (s.overlay && s.overlay !== "none") ? " · " : ""}${s.mode} · ${s.width}×${s.height}${s.overflow.scrollWidth > s.overflow.innerWidth ? ` <span class="warn">⚠ overflow ${s.overflow.scrollWidth}px</span>` : ""}${s.errors.length ? ` <span class="warn">⚠ ${s.errors.length} console error(s)</span>` : ""}</div></div>`).join("")}</div>`).join("")).join("")}
<div class="checklist"><b>Sign-off checklist</b><ul>
<li>Matches the chosen simulation direction (layout, theme, density, radius)?</li>
<li>Light and dark both read well — no hard-coded colors leaking through?</li>
<li>Mobile: no horizontal overflow, nav reachable, tap targets ≥ 40px?</li>
<li>States shown: loading (Skeleton/Spinner), empty (Empty), error (Alert / field errors)?</li>
<li>Focus rings visible when tabbing; contrast ≥ 4.5:1 for text?</li>
<li>Charts: colors from --chart-n via ChartConfig, tooltip + legend present, min-h set?</li>
</ul></div>`
fs.writeFileSync(path.join(out, "index.html"), html)
fs.writeFileSync(path.join(out, "captures.json"), JSON.stringify(shots, null, 2))
console.log(`\nContact sheet: ${path.join(out, "index.html")}`)
