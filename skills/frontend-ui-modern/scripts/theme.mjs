#!/usr/bin/env node
/**
 * theme.mjs — turn a named palette (Catppuccin, Nord, Gruvbox, …) or your own
 * palette file into shadcn/ui CSS variables (`:root` + `.dark`), with WCAG
 * contrast checks. Zero dependencies. Node 18+.
 *
 *   node scripts/theme.mjs list
 *   node scripts/theme.mjs css catppuccin                 # :root (Latte) + .dark (Mocha)
 *   node scripts/theme.mjs css tokyo-night --format oklch # oklch(...) like shadcn's own tokens (default: hex + palette-name comments)
 *   node scripts/theme.mjs css nord --scope data-theme    # [data-theme="nord"] scoped (multi-theme apps)
 *   node scripts/theme.mjs css gruvbox --extras           # also --success/--warning/--info tokens
 *   node scripts/theme.mjs check rose-pine                # contrast report (AA = 4.5:1 text, 3:1 large/UI)
 *   node scripts/theme.mjs css ./my-palette.json          # your own palette (same schema as assets/palettes.json)
 *   node scripts/theme.mjs build                          # regenerate references/themes/*.css + assets/themes.css
 *
 * Palette schema (assets/palettes.json): families → variants (mode, palette{name:hex}, roles{role:name|#hex[@alpha]})
 * and themes[] pairing a light + dark variant under one id. Roles map onto shadcn tokens like so:
 *   bg→background  surface→card,popover  surface2→secondary,muted,accent  border→border,input
 *   fg→foreground(+card/popover/secondary/accent/sidebar-foreground)  fgMuted→muted-foreground
 *   primary→primary,ring,sidebar-primary  primaryFg→primary-foreground  destructive→destructive
 *   chart1..5→chart-1..5  sidebar→sidebar (default: bg)  sidebarAccent→sidebar-accent (default: surface2)
 *   success/warning/info (+Fg) → optional extras (--extras)
 */
import fs from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.resolve(__dirname, "..")
const PALETTES = path.join(ROOT, "assets", "palettes.json")

// ---------- color math ----------------------------------------------------
function parseHex(hex) {
  const h = hex.replace("#", "").trim()
  const full = h.length === 3 ? h.split("").map((c) => c + c).join("") : h
  const n = parseInt(full.slice(0, 6), 16)
  return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 }
}
const srgbToLinear = (c) => {
  c /= 255
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}
function hexToOklch(hex) {
  const { r, g, b } = parseHex(hex)
  const lr = srgbToLinear(r), lg = srgbToLinear(g), lb = srgbToLinear(b)
  const l = Math.cbrt(0.4122214708 * lr + 0.5363325363 * lg + 0.0514459929 * lb)
  const m = Math.cbrt(0.2119034982 * lr + 0.6806995451 * lg + 0.1073969566 * lb)
  const s = Math.cbrt(0.0883024619 * lr + 0.2817188376 * lg + 0.6299787005 * lb)
  const L = 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s
  const A = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s
  const B = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s
  const C = Math.sqrt(A * A + B * B)
  let H = (Math.atan2(B, A) * 180) / Math.PI
  if (H < 0) H += 360
  if (C < 0.0005) H = 0
  return { L, C, H }
}
const fmt = (n, d) => Number(n.toFixed(d)).toString()
function toOklchString(hex, alpha) {
  const { L, C, H } = hexToOklch(hex)
  const a = alpha != null ? ` / ${fmt(alpha * 100, 1)}%` : ""
  return `oklch(${fmt(L, 3)} ${fmt(C, 3)} ${fmt(H, 3)}${a})`
}
function toHexString(hex, alpha) {
  const { r, g, b } = parseHex(hex)
  const h = "#" + [r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("")
  return alpha != null ? `${h}${Math.round(alpha * 255).toString(16).padStart(2, "0")}` : h
}
function luminance(hex) {
  const { r, g, b } = parseHex(hex)
  return 0.2126 * srgbToLinear(r) + 0.7152 * srgbToLinear(g) + 0.0722 * srgbToLinear(b)
}
function blend(fgHex, alpha, bgHex) { // composite a translucent color over an opaque one
  const f = parseHex(fgHex), b = parseHex(bgHex)
  const m = (x, y) => Math.round(x * alpha + y * (1 - alpha))
  return "#" + [m(f.r, b.r), m(f.g, b.g), m(f.b, b.b)].map((v) => v.toString(16).padStart(2, "0")).join("")
}
function contrast(a, b) {
  const la = luminance(a), lb = luminance(b)
  const [hi, lo] = la > lb ? [la, lb] : [lb, la]
  return (hi + 0.05) / (lo + 0.05)
}

// ---------- palette resolution --------------------------------------------
function loadPalettes(file = PALETTES) {
  return JSON.parse(fs.readFileSync(file, "utf8"))
}
/** "name" | "#hex" | "name@0.15" | "#hex@0.15" → { hex, alpha } */
function resolveColor(ref, palette, where) {
  if (ref == null) return null
  let [name, alpha] = String(ref).split("@")
  alpha = alpha != null ? Number(alpha) : undefined
  const hex = name.startsWith("#") ? name : palette[name]
  if (!hex) throw new Error(`Unknown palette color "${name}" in ${where}`)
  return { hex, alpha, name: name.startsWith("#") ? null : name }
}
function resolveVariant(family, variantName, data) {
  const fam = data.families[family]
  if (!fam) throw new Error(`Unknown family "${family}"`)
  const v = fam.variants[variantName]
  if (!v) throw new Error(`Unknown variant "${variantName}" of ${family}`)
  const where = `${family}/${variantName}`
  const r = {}
  for (const [role, ref] of Object.entries(v.roles)) r[role] = resolveColor(ref, v.palette, where)
  // defaults
  r.ring ??= r.primary
  r.sidebar ??= r.bg
  r.sidebarAccent ??= r.surface2
  r.sidebarBorder ??= r.border
  r.input ??= r.border
  r.popover ??= r.surface
  for (const k of ["bg", "surface", "surface2", "border", "fg", "fgMuted", "primary", "primaryFg", "destructive", "chart1", "chart2", "chart3", "chart4", "chart5"])
    if (!r[k]) throw new Error(`Role "${k}" missing in ${where}`)
  return { mode: v.mode, roles: r, title: v.title ?? variantName, derived: !!v.derived }
}

const TOKEN_MAP = [
  ["background", "bg"], ["foreground", "fg"],
  ["card", "surface"], ["card-foreground", "fg"],
  ["popover", "popover"], ["popover-foreground", "fg"],
  ["primary", "primary"], ["primary-foreground", "primaryFg"],
  ["secondary", "surface2"], ["secondary-foreground", "fg"],
  ["muted", "surface2"], ["muted-foreground", "fgMuted"],
  ["accent", "surface2"], ["accent-foreground", "fg"],
  ["destructive", "destructive"],
  ["border", "border"], ["input", "input"], ["ring", "ring"],
  ["chart-1", "chart1"], ["chart-2", "chart2"], ["chart-3", "chart3"], ["chart-4", "chart4"], ["chart-5", "chart5"],
  ["sidebar", "sidebar"], ["sidebar-foreground", "fg"],
  ["sidebar-primary", "primary"], ["sidebar-primary-foreground", "primaryFg"],
  ["sidebar-accent", "sidebarAccent"], ["sidebar-accent-foreground", "fg"],
  ["sidebar-border", "sidebarBorder"], ["sidebar-ring", "ring"],
]
const EXTRA_MAP = [
  ["success", "success"], ["success-foreground", "successFg"],
  ["warning", "warning"], ["warning-foreground", "warningFg"],
  ["info", "info"], ["info-foreground", "infoFg"],
]

function tokensFor(variant, { format = "hex", extras = false } = {}) {
  const conv = format === "oklch" ? toOklchString : toHexString
  const note = (c) => (c.name ? `${c.name}${c.alpha != null ? ` @ ${Math.round(c.alpha * 100)}%` : ""}` : c.alpha != null ? `@ ${Math.round(c.alpha * 100)}%` : "")
  const out = []
  for (const [token, role] of TOKEN_MAP) {
    const c = variant.roles[role]
    out.push([token, conv(c.hex, c.alpha), note(c)])
  }
  if (extras) {
    for (const [token, role] of EXTRA_MAP) {
      const c = variant.roles[role]
      if (c) out.push([token, conv(c.hex, c.alpha), note(c)])
    }
  }
  return out
}

function cssBlock(selector, tokens, { radius } = {}) {
  const lines = [`${selector} {`]
  if (radius) lines.push(`  --radius: ${radius};`)
  const w = Math.max(...tokens.map(([t, v]) => t.length + v.length))
  for (const [t, v, note] of tokens) lines.push(`  --${t}: ${v};${note ? " ".repeat(Math.max(1, w - t.length - v.length + 2)) + `/* ${note} */` : ""}`)
  lines.push("}")
  return lines.join("\n")
}

function themeCss(theme, data, opts = {}) {
  const { scope = "root", format = "hex", extras = false, radius = "0.625rem" } = opts
  const light = resolveVariant(theme.family, theme.light, data)
  const dark = resolveVariant(theme.family, theme.dark, data)
  const fam = data.families[theme.family]
  let lightSel, darkSel
  if (scope === "root") { lightSel = ":root"; darkSel = ".dark" }
  else if (scope === "data-theme") {
    lightSel = `[data-theme="${theme.id}"]`
    darkSel = `[data-theme="${theme.id}"].dark, .dark [data-theme="${theme.id}"]`
  } else if (scope === "class") {
    lightSel = `.theme-${theme.id}`
    darkSel = `.theme-${theme.id}.dark, .dark .theme-${theme.id}`
  } else throw new Error(`Unknown scope ${scope}`)
  const header = [
    `/* ${theme.title} — light: ${light.title}${light.derived ? " (derived)" : ""}, dark: ${dark.title}${dark.derived ? " (derived)" : ""}`,
    `   Source: ${fam.source}`,
    `   Generated by frontend-ui-modern/scripts/theme.mjs — tokens follow the shadcn/ui contract (ui.shadcn.com/docs/theming) */`,
  ].join("\n")
  const parts = [
    header,
    cssBlock(lightSel, tokensFor(light, { format, extras }), { radius: scope === "root" ? radius : undefined }),
    cssBlock(darkSel, tokensFor(dark, { format, extras })),
  ]
  if (extras) {
    parts.push(
      `/* Expose extras to Tailwind (add once, next to the other @theme inline mappings):\n@theme inline {\n  --color-success: var(--success);\n  --color-success-foreground: var(--success-foreground);\n  --color-warning: var(--warning);\n  --color-warning-foreground: var(--warning-foreground);\n  --color-info: var(--info);\n  --color-info-foreground: var(--info-foreground);\n} */`
    )
  }
  return parts.join("\n\n") + "\n"
}

// ---------- contrast report ----------------------------------------------
function checkVariant(variant, label) {
  const r = variant.roles
  const pairs = [
    ["foreground on background", r.fg, r.bg, 4.5],
    ["foreground on card", r.fg, r.surface, 4.5],
    ["muted-foreground on background", r.fgMuted, r.bg, 4.5],
    ["muted-foreground on muted", r.fgMuted, r.surface2, 4.5],
    ["primary-foreground on primary", r.primaryFg, r.primary, 4.5],
    ["primary on background (links/icons)", r.primary, r.bg, 3],
    ["destructive on background", r.destructive, r.bg, 3],
    ["border on background (UI)", r.border, r.bg, 1.3],
    ["foreground on sidebar", r.fg, r.sidebar, 4.5],
  ]
  const rows = []
  let fails = 0
  for (const [name, a, b, min] of pairs) {
    if (!a || !b) continue
    const bgHex = b.alpha != null ? blend(b.hex, b.alpha, r.bg.hex) : b.hex
    const fgHex = a.alpha != null ? blend(a.hex, a.alpha, bgHex) : a.hex
    const c = contrast(fgHex, bgHex)
    const ok = c >= min
    if (!ok) fails++
    rows.push(`  ${ok ? "ok  " : "WARN"} ${c.toFixed(2).padStart(5)}:1  (min ${min})  ${name}`)
  }
  return { text: `${label} (${variant.mode}):\n${rows.join("\n")}`, fails }
}

// ---------- CLI -----------------------------------------------------------
function parseArgs(argv) {
  const args = { _: [] }
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    if (a.startsWith("--")) {
      const k = a.slice(2)
      const next = argv[i + 1]
      if (next && !next.startsWith("--")) { args[k] = next; i++ } else args[k] = true
    } else args._.push(a)
  }
  return args
}

function findTheme(idOrFile, data) {
  const t = data.themes.find((t) => t.id === idOrFile)
  if (t) return { theme: t, data }
  if (fs.existsSync(idOrFile)) {
    const custom = loadPalettes(idOrFile)
    const merged = { families: { ...data.families, ...custom.families }, themes: [...(custom.themes || [])] }
    const ct = merged.themes[0]
    if (!ct) throw new Error(`No themes[] entry in ${idOrFile}`)
    return { theme: ct, data: merged }
  }
  throw new Error(`Unknown theme "${idOrFile}". Run: node scripts/theme.mjs list`)
}

function main() {
  const args = parseArgs(process.argv.slice(2))
  const cmd = args._[0]
  const data = loadPalettes()
  if (!cmd || cmd === "help") {
    console.log(fs.readFileSync(fileURLToPath(import.meta.url), "utf8").split("*/")[0].replace(/^\/\*\*?/, ""))
    return
  }
  if (cmd === "list") {
    const byFam = {}
    for (const t of data.themes) (byFam[t.family] ??= []).push(t)
    for (const [fam, themes] of Object.entries(byFam)) {
      const f = data.families[fam]
      console.log(`${f.title}  —  ${f.vibe ?? ""}`)
      for (const t of themes) console.log(`  ${t.id.padEnd(22)} light: ${t.light.padEnd(10)} dark: ${t.dark.padEnd(10)} ${t.note ?? ""}`)
    }
    console.log(`\nshadcn built-ins (base colors + accent themes): see references/themes/shadcn-base-colors.css and shadcn-accents.css`)
    return
  }
  if (cmd === "css") {
    const { theme, data: d } = findTheme(args._[1], data)
    process.stdout.write(themeCss(theme, d, { scope: args.scope, format: args.format, extras: !!args.extras, radius: args.radius }))
    return
  }
  if (cmd === "check") {
    const { theme, data: d } = findTheme(args._[1], data)
    const l = checkVariant(resolveVariant(theme.family, theme.light, d), `${theme.title} / ${theme.light}`)
    const k = checkVariant(resolveVariant(theme.family, theme.dark, d), `${theme.title} / ${theme.dark}`)
    console.log(l.text + "\n\n" + k.text)
    console.log(`\n${l.fails + k.fails === 0 ? "All pairs pass." : `${l.fails + k.fails} pair(s) below target — pick a lighter/darker palette slot for that role or accept for decorative use.`}`)
    return
  }
  if (cmd === "build") {
    const outDir = path.join(ROOT, "references", "themes")
    fs.mkdirSync(outDir, { recursive: true })
    const byFam = {}
    for (const t of data.themes) (byFam[t.family] ??= []).push(t)
    const all = []
    let warnings = []
    for (const [fam, themes] of Object.entries(byFam)) {
      const f = data.families[fam]
      const chunks = [`/* ${f.title} themes for shadcn/ui. Source: ${f.source}\n   Each theme below is a complete :root + .dark pair. Copy ONE pair into app/globals.css\n   (replacing the generated :root/.dark blocks). For multi-theme apps use the [data-theme] file: assets/themes.css */\n`]
      for (const t of themes) {
        chunks.push(`/* ======== ${t.id} ======== */`)
        chunks.push(themeCss(t, data, { scope: "root", extras: true }))
        all.push(themeCss(t, data, { scope: "data-theme", extras: true }))
        for (const v of [t.light, t.dark]) {
          const res = checkVariant(resolveVariant(fam, v, data), `${t.id}/${v}`)
          if (res.fails) warnings.push(res.text)
        }
      }
      fs.writeFileSync(path.join(outDir, `${fam}.css`), chunks.join("\n"))
    }
    const simCss = path.join(ROOT, "assets", "themes.css")
    fs.writeFileSync(simCss, `/* All themes, scoped by [data-theme="<id>"] (+ .dark). Generated by scripts/theme.mjs build. */\n\n` + all.join("\n"))
    // shadcn built-ins
    const builtins = JSON.parse(fs.readFileSync(path.join(ROOT, "assets", "shadcn-themes.json"), "utf8"))
    const base = [], accents = []
    for (const [name, th] of Object.entries(builtins)) {
      const isBase = ["neutral", "stone", "zinc", "mauve", "olive", "mist", "taupe"].includes(name)
      const mk = (sel, vars) => `${sel} {\n${Object.entries(vars).map(([k, v]) => `  --${k}: ${v};`).join("\n")}\n}`
      const block = `/* ======== shadcn ${isBase ? "base color" : "accent theme"}: ${th.title} ======== */\n${mk(isBase ? ":root" : `:root /* ${name}: overrides on top of a base color */`, th.light)}\n${mk(".dark", th.dark)}\n`
      ;(isBase ? base : accents).push(block)
    }
    fs.writeFileSync(path.join(outDir, "shadcn-base-colors.css"), `/* shadcn/ui base colors (what \`npx shadcn init --base-color <name>\` / presets generate). Source: apps/v4/registry/themes.ts */\n\n` + base.join("\n"))
    fs.writeFileSync(path.join(outDir, "shadcn-accents.css"), `/* shadcn/ui accent themes (primary + secondary + chart + sidebar-primary overrides). Apply ON TOP of a base color. Source: apps/v4/registry/themes.ts */\n\n` + accents.join("\n"))
    console.log(`Wrote ${Object.keys(byFam).length} family files + shadcn-base-colors.css + shadcn-accents.css to references/themes/, and assets/themes.css (${all.length} themes).`)
    if (warnings.length) console.log(`\nContrast notes (decorative use is fine; for body text prefer the suggested fix):\n` + warnings.join("\n\n"))
    return
  }
  throw new Error(`Unknown command ${cmd}`)
}

try { main() } catch (e) { console.error("theme.mjs:", e.message); process.exit(1) }
