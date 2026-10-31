# Pre-ship checklist

Work through it once, in order. Each line says how to check, not just what.

## Theme and tokens
- [ ] The theme was chosen from the brief's subject and job, and named in the hand-over with the reason.
- [ ] Every colour, radius, border, shadow and spacing value in the page reads a token. `grep -n "#[0-9a-f]\{6\}" page.css` outside the token block should find nothing (or only `#fff` inside a `--bad-btn` rule).
- [ ] Lengths that can be zero are `0px`, not `0`.
- [ ] `color-scheme` matches the theme so selects, checkboxes and scrollbars are not the wrong mode.
- [ ] Fonts: the theme's families are linked with real fallback stacks; numbers are in the UI sans.

## Contrast and colour
- [ ] `node scripts/check_theme.js <css>` reports no FAIL. (Body text ≥ 4.5:1, muted ≥ 3:1, button labels ≥ 4.5:1, accent-as-text ≥ 4.5:1, semantic text ≥ 4.5:1, chart marks ≥ 3:1, series in band and separable.)
- [ ] Semantic colour (good/warn/bad) is never a chart series and never the accent.
- [ ] State is never colour-alone: pills carry a shape and a label; alerts carry an icon and a title.

## Layout and space
- [ ] One memorable element on the first screen; everything else quiet.
- [ ] White space measured (`scripts/measure_whitespace.js --selector .main`) and inside roughly 55–68% for a tool page; article pages may read higher.
- [ ] Reading text ≤ 65–72ch; headings `text-wrap: balance`.
- [ ] Panels use one mechanism (rule *or* border *or* tint), never two.
- [ ] Sidebar nav bleeds to the edge; nav text aligns with the logo mark.

## Charts
- [ ] Style matches the theme group (editorial: tab, right-hand labels, names in the plot; tool: left labels, legend, end values).
- [ ] 2px lines, ringed end markers, hairline solid grid, one axis, legend for 2+ series, text in text tokens.
- [ ] Hover tooltip works; labels do not collide at the right edge.

## Quality floor
- [ ] Renders at 400px without horizontal scrolling; sidebar becomes a scrolling strip; grids stack.
- [ ] Keyboard: every control reachable, `:focus-visible` outline in the accent visible.
- [ ] `prefers-reduced-motion` respected; no motion that does not answer an action except at most one orchestrated moment.
- [ ] No clipped text at any width; long labels wrap or scroll.
- [ ] Screenshot taken and compared to the matching reference in `assets/screenshots/`.

## Avoid list walked
- [ ] No cream + serif + terracotta default, no black + acid accent, no card kit, no eyebrow caps, no "→" on buttons, no gradient washes, no serif numerals, no numbered markers on non-sequences, no second red in an editorial view, no shadows in an editorial theme.
