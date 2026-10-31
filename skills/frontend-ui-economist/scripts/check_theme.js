#!/usr/bin/env node
/* frontend-ui-economist / check_theme.js
   Checks a theme's token block the way a reviewer would, but computed instead of eyeballed.
   No dependencies.

   Usage:
     node check_theme.js assets/theme-kit.css                 # checks every [data-theme="…"] block in the file
     node check_theme.js assets/theme-kit.css --theme airy    # one theme
     node check_theme.js my-theme.css                         # any CSS with a block that sets --bg, --ink, … tokens
     node check_theme.js --json … (machine-readable)

   What it checks (and why):
     1. Text contrast (WCAG 2.x). Body text (--ink, --ink-2) >= 4.5:1 on --bg and --surface; muted text
        (--ink-3) >= 3:1 (it is used for small labels, so keep it readable but it can be quieter);
        --accent-ink on --accent >= 4.5:1 (button labels); --accent-text on --bg >= 4.5:1 (links, kickers);
        --good/--warn/--bad on --bg >= 4.5:1 (they are used as delta text); sidebar --side-ink on --side-bg >= 4.5:1.
     2. Chart series (--s1..--s4): each >= 3:1 against --surface (marks), OKLCH lightness inside the band for the
        mode (light 0.43–0.77, dark 0.48–0.67), chroma >= 0.10 so hues read as colour, and adjacent pairs separated
        under simulated protanopia/deuteranopia (OKLab ΔE×100 >= 8 target, 6 floor) and under normal vision (>= 15).
        These thresholds follow the data-viz method the themes were built with; the CVD simulation uses the
        Machado, Oliveira & Fernandes (2009) matrices at severity 1.0.
   Exit code 1 on any FAIL. WARN does not fail.
*/
'use strict';
const fs = require('fs');

// ---------- colour maths ----------
function parseHex(h) {
  h = String(h).trim();
  const m = h.match(/^#?([0-9a-f]{3}|[0-9a-f]{6})$/i);
  if (!m) return null;
  let s = m[1]; if (s.length === 3) s = s.split('').map(c => c + c).join('');
  return [0, 2, 4].map(i => parseInt(s.slice(i, i + 2), 16) / 255);
}
const lin = c => c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
const gam = c => c <= 0.0031308 ? 12.92 * c : 1.055 * Math.pow(Math.max(0, c), 1 / 2.4) - 0.055;
function luminance(rgb) { const [r, g, b] = rgb.map(lin); return 0.2126 * r + 0.7152 * g + 0.0722 * b; }
function contrast(a, b) { const la = luminance(a), lb = luminance(b); const hi = Math.max(la, lb), lo = Math.min(la, lb); return (hi + 0.05) / (lo + 0.05); }
function oklab(rgb) {
  const [r, g, b] = rgb.map(lin);
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * s,
          1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * s,
          0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * s];
}
const chroma = lab => Math.hypot(lab[1], lab[2]);
const dE = (a, b) => 100 * Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2]);
// Machado et al. 2009, severity 1.0, applied in linear RGB
const CVD = {
  protan: [[0.152286, 1.052583, -0.204868], [0.114503, 0.786281, 0.099216], [-0.003882, -0.048116, 1.051998]],
  deutan: [[0.367322, 0.860646, -0.227968], [0.280085, 0.672501, 0.047413], [-0.011820, 0.042940, 0.968881]],
  tritan: [[1.255528, -0.076749, -0.178779], [-0.078411, 0.930809, 0.147602], [0.004733, 0.691367, 0.303900]]
};
function simulate(rgb, kind) {
  const l = rgb.map(lin); const M = CVD[kind];
  const out = M.map(row => row[0] * l[0] + row[1] * l[1] + row[2] * l[2]);
  return out.map(v => Math.min(1, Math.max(0, gam(v))));
}

// ---------- CSS token parsing ----------
function parseThemes(css) {
  const themes = {};
  const re = /\[data-theme="([^"]+)"\]\s*\{([^}]*)\}/g; let m;
  while ((m = re.exec(css))) { themes[m[1]] = Object.assign(themes[m[1]] || {}, parseTokens(m[2])); }
  if (!Object.keys(themes).length) {
    // any block that declares --bg: treat the whole file as one theme
    const blocks = css.match(/\{[^}]*--bg\s*:[^}]*\}/g) || [];
    blocks.forEach((b, i) => { themes['theme' + (i + 1)] = parseTokens(b.slice(1, -1)); });
  }
  return themes;
}
function parseTokens(body) {
  const t = {}; const re = /(--[a-z0-9-]+)\s*:\s*([^;]+);/gi; let m;
  while ((m = re.exec(body))) t[m[1]] = m[2].trim();
  return t;
}
function resolve(t, name, depth = 0) {
  let v = t[name]; if (v == null || depth > 5) return null;
  const vm = v.match(/^var\((--[a-z0-9-]+)\)$/i); if (vm) return resolve(t, vm[1], depth + 1);
  return v;
}

// ---------- checks ----------
function check(name, t) {
  const get = n => { const v = resolve(t, n); return v ? parseHex(v) : null; };
  const rows = []; let fails = 0, warns = 0;
  const pair = (fg, bg, min, label, warnOnly) => {
    const a = get(fg), b = get(bg); if (!a || !b) { rows.push({ check: label, status: 'SKIP', detail: `${fg} or ${bg} not a hex colour` }); return; }
    const c = contrast(a, b); const ok = c >= min;
    const status = ok ? 'PASS' : (warnOnly ? 'WARN' : 'FAIL'); if (status === 'FAIL') fails++; if (status === 'WARN') warns++;
    rows.push({ check: label, status, detail: `${fg} on ${bg} = ${c.toFixed(2)}:1 (min ${min})` });
  };
  pair('--ink', '--bg', 4.5, 'Body text on page');
  pair('--ink', '--surface', 4.5, 'Body text on panels');
  pair('--ink-2', '--bg', 4.5, 'Secondary text on page');
  pair('--ink-2', '--surface', 4.5, 'Secondary text on panels');
  pair('--ink-3', '--bg', 3, 'Muted text on page');
  pair('--accent-ink', '--accent', 4.5, 'Button label on accent');
  pair('--accent-text', '--bg', 4.5, 'Accent used as text');
  pair('--good', '--bg', 4.5, 'Good (delta text) on page');
  pair('--warn', '--bg', 4.5, 'Warn (delta text) on page');
  pair('--bad', '--bg', 4.5, 'Bad (delta text) on page');
  pair('--side-ink', '--side-bg', 4.5, 'Sidebar text');
  pair('--side-ink-2', '--side-bg', 4.5, 'Sidebar secondary text');

  // chart series
  const surface = get('--surface') || get('--bg');
  const dark = surface && luminance(surface) < 0.2;
  const band = dark ? [0.48, 0.67] : [0.43, 0.77];
  const slots = ['--s1', '--s2', '--s3', '--s4'].map(n => ({ n, rgb: get(n) })).filter(s => s.rgb);
  slots.forEach(s => {
    const c = contrast(s.rgb, surface); const lab = oklab(s.rgb);
    const L = lab[0], C = chroma(lab);
    const notes = [];
    let status = 'PASS';
    if (c < 3) { status = 'WARN'; notes.push(`contrast ${c.toFixed(2)}:1 < 3 (needs visible labels or a table view)`); }
    if (L < band[0] || L > band[1]) { status = 'FAIL'; notes.push(`OKLCH L ${L.toFixed(2)} outside ${band[0]}–${band[1]} (${dark ? 'dark' : 'light'} band)`); }
    if (C < 0.10) { status = 'FAIL'; notes.push(`chroma ${C.toFixed(3)} < 0.10 (reads as grey)`); }
    if (status === 'FAIL') fails++; if (status === 'WARN') warns++;
    rows.push({ check: `Series ${s.n}`, status, detail: notes.length ? notes.join('; ') : `contrast ${c.toFixed(2)}:1, L ${L.toFixed(2)}, C ${C.toFixed(3)}` });
  });
  for (let i = 1; i < slots.length; i++) {
    const a = slots[i - 1], b = slots[i];
    const normal = dE(oklab(a.rgb), oklab(b.rgb));
    const cvd = {}; ['protan', 'deutan', 'tritan'].forEach(k => { cvd[k] = dE(oklab(simulate(a.rgb, k)), oklab(simulate(b.rgb, k))); });
    const worst = Math.min(cvd.protan, cvd.deutan);
    let status = 'PASS'; const notes = [];
    if (normal < 15) { status = 'FAIL'; notes.push(`normal-vision ΔE ${normal.toFixed(1)} < 15`); }
    if (worst < 6) { status = 'FAIL'; notes.push(`CVD ΔE ${worst.toFixed(1)} < 6`); }
    else if (worst < 8) { if (status !== 'FAIL') status = 'WARN'; notes.push(`CVD ΔE ${worst.toFixed(1)} in the 6–8 floor band (needs direct labels or texture)`); }
    if (status === 'FAIL') fails++; if (status === 'WARN') warns++;
    rows.push({ check: `Adjacent ${a.n} ↔ ${b.n}`, status, detail: notes.length ? notes.join('; ') : `ΔE normal ${normal.toFixed(1)}, protan ${cvd.protan.toFixed(1)}, deutan ${cvd.deutan.toFixed(1)}, tritan ${cvd.tritan.toFixed(1)}` });
  }
  return { theme: name, mode: dark ? 'dark' : 'light', fails, warns, rows };
}

// ---------- cli ----------
const args = process.argv.slice(2);
const file = args.find(a => !a.startsWith('--'));
const only = (args.find(a => a.startsWith('--theme=')) || '').slice(8) || (args.includes('--theme') ? args[args.indexOf('--theme') + 1] : null);
const json = args.includes('--json');
if (!file) { console.error('usage: node check_theme.js <css-file> [--theme name] [--json]'); process.exit(2); }
const themes = parseThemes(fs.readFileSync(file, 'utf8'));
const names = Object.keys(themes).filter(n => !only || n === only);
if (!names.length) { console.error('no theme blocks found' + (only ? ` for "${only}"` : '')); process.exit(2); }
const results = names.map(n => check(n, themes[n]));
if (json) { console.log(JSON.stringify(results, null, 2)); }
else {
  for (const r of results) {
    console.log(`\n== ${r.theme} (${r.mode})  ${r.fails ? r.fails + ' FAIL' : 'no fails'}${r.warns ? ', ' + r.warns + ' warn' : ''}`);
    for (const row of r.rows) console.log(`  [${row.status}] ${row.check.padEnd(30)} ${row.detail}`);
  }
}
process.exit(results.some(r => r.fails) ? 1 : 0);
