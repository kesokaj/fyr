# Further reading and provenance

Where the ideas in this skill come from, so you can go deeper or check a claim. Nothing here is required
reading; the skill is self-contained.

## The Economist's visual language (the editorial themes)

- *The Economist visual styleguide* (chart style guide, v1.2, 2017, public PDF):
  https://d30jyndnpz0ndz.cloudfront.net/documents/CHARTstyleguide_20170505.pdf
  — the primary source for the editorial chart conventions: the small red rectangle above a chart, y-axis
  labels on the right with no axis line, gridlines kept, a "Source:" line, red used to mark the one
  series or bar the story is about.
- *Graphic detail*, The Economist's data-journalism section, for living examples:
  https://www.economist.com/graphic-detail
- Trafford Data Lab, *Developing a data visualisation style* (how The Economist, FT and FiveThirtyEight
  turn conventions into a recognisable house style):
  https://medium.com/@traffordDataLab/developing-a-data-visualisation-style-cd24f88fa59
- FlowingData on Sarah Leo's "Mistakes, we've drawn a few" (The Economist redesigning its own charts):
  https://flowingdata.com/?p=55716

What the themes borrow is the *vocabulary* (rules instead of boxes, one red, serif + sans, right-hand
axis labels, the tab, the source line) and nothing proprietary: no wordmark, no masthead box, and the
typefaces are open ones that share the spirit (Fraunces, Newsreader, Source Serif 4) rather than the
paper's own faces.

## Google and white space (the Airy theme)

- Material Design 3: https://m3.material.io/ — layout and spacing:
  https://m3.material.io/foundations/layout/applying-layout ; shape:
  https://m3.material.io/styles/shape/overview
- The 60-30-10 rule as applied to UI colour (60% dominant neutral, 30% secondary surface, 10% accent),
  which is where the "60%" in "60% white space" usually comes from:
  https://blog.logrocket.com/?p=179177
- Airy takes from Material the 8px grid, outlined cards, pill buttons, the half-pill navigation item and the
  single blue, and treats white space as the structural element. See `white-space.md` for the measured
  numbers.

## Colour and accessibility

- WCAG 2.2, *Understanding Success Criterion 1.4.3: Contrast (Minimum)*:
  https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html — the 4.5:1 / 3:1 thresholds used by
  `scripts/check_theme.js`.
- Björn Ottosson, *A perceptual color space for image processing* (Oklab, 2020):
  https://bottosson.github.io/posts/oklab/ — the space in which the chart palettes' lightness, chroma and
  separation are measured.
- Machado, Oliveira & Fernandes, *A Physiologically-based Model for Simulation of Color Vision
  Deficiency*, IEEE TVCG 15(6), 2009: https://www.inf.ufrgs.br/~oliveira/pubs_files/CVD_Simulation/CVD_Simulation.html
  — the simulation matrices (severity 1.0) in `check_theme.js`.

## Typography

- Robert Bringhurst, *The Elements of Typographic Style* — the type-scale and measure guidance (65ch lines,
  a little more leading for serifs) behind `--fs-*` and `--prose-lh`.
- The typefaces, all on Google Fonts under open licences:
  Fraunces https://fonts.google.com/specimen/Fraunces ·
  Newsreader https://fonts.google.com/specimen/Newsreader ·
  Source Serif 4 https://fonts.google.com/specimen/Source+Serif+4 ·
  Source Sans 3 https://fonts.google.com/specimen/Source+Sans+3 ·
  IBM Plex Sans https://fonts.google.com/specimen/IBM+Plex+Sans ·
  Figtree https://fonts.google.com/specimen/Figtree ·
  Manrope https://fonts.google.com/specimen/Manrope ·
  DM Sans https://fonts.google.com/specimen/DM+Sans

## Method

- The anti-pattern list and the "spend boldness in one place" discipline follow Anthropic's
  `frontend-design` skill; the chart mark rules, the four colour jobs and the six palette checks follow its
  `dataviz` skill (whose validator agrees with `check_theme.js` to the decimal on the palettes here).
- The simulator the screenshots come from is `../assets/theme-simulator.html`; it is also published as a
  claude.ai artifact ("Theme Simulator") so the themes can be browsed without running anything.
