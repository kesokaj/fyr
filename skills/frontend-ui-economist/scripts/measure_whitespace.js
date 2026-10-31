#!/usr/bin/env node
/* frontend-ui-economist / measure_whitespace.js
   Estimates how much of a page (or one region of it) is white space, the same way the theme simulator's
   readout does, and can save a screenshot with the counted "ink" overlaid so you can see what was measured.

   Requires Playwright with Chromium available (npm i playwright && npx playwright install chromium, or a
   preinstalled one). Everything else is plain Node.

   Usage:
     node measure_whitespace.js <url-or-file.html> [--selector .main] [--width 1280] [--height 800]
                                                   [--overlay out.png] [--theme airy] [--ws 1.4]
   Examples:
     node measure_whitespace.js assets/starter.html --selector .main --overlay /tmp/ink.png
     node measure_whitespace.js https://example.com/app --selector main

   How it counts (keep this in mind when reading the number):
     - "Ink" = text line boxes, form controls and buttons, images and SVG charts, pills/badges/swatches.
     - Everything else inside the measured region's visible viewport is white space: margins, padding,
       gaps, empty panel area. Borders and hairlines are not ink.
     - Only the visible viewport is measured (what a person sees on first paint), clipped to --selector.
     - It is an estimate for conversation and comparison, not a spec: a page with a big chart scores lower
       than the same layout without one. Compare like with like, and aim for roughly 55–68% on a tool page.
*/
'use strict';
const path = require('path');
const args = process.argv.slice(2);
const target = args.find(a => !a.startsWith('--'));
const opt = (name, def) => { const i = args.indexOf('--' + name); return i >= 0 ? args[i + 1] : def; };
if (!target) { console.error('usage: node measure_whitespace.js <url-or-file> [--selector css] [--width n] [--height n] [--overlay out.png] [--theme name] [--ws n]'); process.exit(2); }
const selector = opt('selector', 'body'), width = +opt('width', 1280), height = +opt('height', 800), overlay = opt('overlay', null), theme = opt('theme', null), ws = opt('ws', null);
const url = /^https?:\/\//.test(target) ? target : 'file://' + path.resolve(target);

(async () => {
  let chromium;
  try { ({ chromium } = require('playwright')); } catch (e) { console.error('Playwright is not installed: npm i playwright && npx playwright install chromium'); process.exit(2); }
  const browser = await chromium.launch();
  const page = await (await browser.newContext({ viewport: { width, height } })).newPage();
  await page.goto(url, { waitUntil: 'load' });
  if (theme || ws) await page.evaluate(({ theme, ws }) => { const root = document.querySelector('[data-theme]') || document.body; if (theme) root.dataset.theme = theme; if (ws) root.style.setProperty('--ws', ws); }, { theme, ws });
  await page.waitForTimeout(600);
  await page.evaluate(() => document.fonts && document.fonts.ready);

  const result = await page.evaluate(({ selector, draw }) => {
    const region = document.querySelector(selector) || document.body;
    const rr = region.getBoundingClientRect();
    const vp = { left: Math.max(0, rr.left), top: Math.max(0, rr.top), right: Math.min(innerWidth, rr.right), bottom: Math.min(innerHeight, rr.bottom) };
    const area = (vp.right - vp.left) * (vp.bottom - vp.top); if (area <= 0) return { error: 'region not visible' };
    const INK = 'button,input,select,textarea,svg,img,canvas,video,.pill,.badge,.switch i,.avatar,.tab,.swatch i,.toast,.alert .ic';
    let ink = 0; const boxes = [];
    const add = r => { const x1 = Math.max(r.left, vp.left), y1 = Math.max(r.top, vp.top), x2 = Math.min(r.right, vp.right), y2 = Math.min(r.bottom, vp.bottom); if (x2 > x1 && y2 > y1) { ink += (x2 - x1) * (y2 - y1); boxes.push([x1, y1, x2 - x1, y2 - y1]); } };
    const walk = node => {
      if (node.nodeType === 3) { if (!node.textContent.trim()) return; const rg = document.createRange(); rg.selectNodeContents(node); for (const r of rg.getClientRects()) if (r.width >= 1 && r.height >= 1) add(r); return; }
      if (node.nodeType !== 1) return; const el = node; if (el.id === 'ws-ink-overlay') return;
      const cs = getComputedStyle(el); if (cs.display === 'none' || cs.visibility === 'hidden') return;
      const r = el.getBoundingClientRect(); if (r.bottom < vp.top || r.top > vp.bottom) return;
      if (el.matches(INK)) { add(r); return; }
      for (const c of el.childNodes) walk(c);
    };
    walk(region);
    const white = Math.max(0, 1 - ink / area);
    if (draw) {
      const ov = document.createElement('div'); ov.id = 'ws-ink-overlay'; ov.style.cssText = 'position:fixed;inset:0;pointer-events:none;z-index:2147483647';
      boxes.forEach(b => { const i = document.createElement('i'); i.style.cssText = `position:absolute;left:${b[0]}px;top:${b[1]}px;width:${b[2]}px;height:${b[3]}px;background:rgba(255,0,110,.28);outline:1px solid rgba(255,0,110,.45)`; ov.appendChild(i); });
      document.body.appendChild(ov);
    }
    return { selector, region: { width: Math.round(vp.right - vp.left), height: Math.round(vp.bottom - vp.top) }, ink_boxes: boxes.length, white_space_pct: Math.round(white * 100) };
  }, { selector, draw: !!overlay });

  if (result.error) { console.error(result.error); await browser.close(); process.exit(1); }
  if (overlay) { await page.screenshot({ path: overlay }); result.overlay = overlay; }
  await browser.close();
  console.log(JSON.stringify(result, null, 2));
  const pct = result.white_space_pct;
  console.log(`\n≈${pct}% white space in "${selector}" (${pct >= 55 && pct <= 68 ? 'inside' : 'outside'} the 55–68% band around the 60% rule of thumb)`);
})();
