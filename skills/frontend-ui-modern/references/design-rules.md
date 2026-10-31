# Design rules for shadcn-based apps

These keep an app looking intentional rather than "default template". They are opinions that match how shadcn's own
blocks are built; break them only with a reason the operator agreed to.

## Picking a style (geometry) before anything else

| Style | Pick when | Pair with |
| --- | --- | --- |
| `nova` (default) | product/admin UI, 32–36px controls, hairlines over shadows | Geist / Inter |
| `vega` | the classic shadcn look, conventional SaaS | Inter |
| `mira` | dense data tools, 28–32px controls, tight tables | Inter / IBM Plex Sans |
| `rhea` | soft shapes but compact; focused product surfaces | Inter |
| `luma` | consumer apps, rounded, soft layered elevation (macOS-like) | Inter / Figtree |
| `maia` | airy marketing-adjacent apps, 40px controls, 12px+ card radius | Figtree / Manrope |
| `lyra` | terminal/dev tools, square corners, mono type | JetBrains Mono / Geist Mono |
| `sera` | editorial/content products, serif headings, uppercase labels, underlined controls | Noto Sans + Playfair Display |

Decide this in the simulation (toolbar "style") — it changes the feel more than any color.

## Layout

- App shell: sidebar (16rem, icon-collapsible) + header (3rem: SidebarTrigger, Breadcrumb, actions on the right) + content with
  `flex flex-col gap-4 p-4 md:gap-6 md:p-6`. Inset variant (`SidebarProvider` + `variant="inset"` + `SidebarInset`) when the sidebar should read as a chrome.
- Content width: dashboards fill; forms and settings max `~72rem` (`max-w-6xl mx-auto`); reading content `~65ch`.
- Grids: KPI cards `grid-cols-1 @xl/main:grid-cols-2 @5xl/main:grid-cols-4` (container queries on the main area); two-column pages `lg:grid-cols-[2fr_1fr]`.
- Spacing scale: 4 / 8 / 12 / 16 / 24 / 32 px (`gap-1 … gap-8`); cards use `--card-spacing` (24px, 16px for `size="sm"`); never mix ad-hoc values.
- Alignment: labels above inputs; actions right-aligned in headers/footers; primary action last (rightmost) and only one per view.

## Typography

- Body `text-sm` (14px) in app UI, `text-base` in content; page title `text-2xl font-semibold tracking-tight`, section title `text-base font-medium`,
  descriptions `text-sm text-muted-foreground`. KPI values `text-2xl font-semibold tabular-nums` (3xl when the card is wide).
- One sans for everything unless the style says otherwise (sera: serif headings; lyra: mono). Numbers and tables: `tabular-nums`.
- Line length and wrapping: `text-balance` for titles, `text-pretty` for paragraphs, `truncate` for single-line cells, `line-clamp-2` for descriptions.

## Color usage

- Surfaces: background → card → popover. Use `muted` for grouped/secondary areas (tabs list, table header hover), `accent` for interactive hover only.
- One primary action per screen in `primary`; everything else outline/ghost/secondary. Destructive actions are tinted (`variant="destructive"` is already soft in 2026 styles).
- Status: `Badge` variants or `success/warning/info/destructive` tokens at 10–15% tint with the solid color as text; never raw Tailwind palette colors.
- Charts: `--chart-1..5` in order; single-series → `--primary`; gradients 0.8→0.1 opacity like the examples.
- Dark mode is a token swap, nothing else: no `dark:` classes in app code.

## Density and controls

- Control height follows the style (`h-9` nova default, `h-8` sm, `h-10` lg); don't mix sizes in one row.
- Tables: `text-sm`, 40px header, 36–44px rows, first/last cell padding 16px, hover row `bg-muted/50`, sticky header for long lists, right-align numbers.
- Forms: `FieldGroup` gap 24px; two-column grids for short related fields (name/slug, city/zip); full width for text areas; help text under the control;
  errors replace help text; disabled explains itself with a tooltip.

## States and feedback

- Loading: `Skeleton` with the real layout's shape (cards, rows), not a spinner in the middle; buttons show `Spinner` + disabled during submit.
- Empty: `Empty` with icon, one-sentence explanation, one primary action; filters that return nothing get a "clear filters" action.
- Errors: inline for fields; `Alert variant="destructive"` for page-level; toast for transient; never only a toast for a form error.
- Success: toast with an undo when the action is reversible; optimistic updates for toggles.
- Confirmations: `AlertDialog` only for destructive/irreversible actions; otherwise act and offer undo.

## Motion

- Use what the components ship (`tw-animate-css`, `data-open`/`data-closed` variants). Durations 100–200ms; no entrance animations on page load;
  respect `prefers-reduced-motion` (the shipped animations already do via Tailwind's `motion-reduce` where applicable — don't add custom keyframes).

## Responsiveness

- Breakpoints used by blocks: 768px (sidebar → Sheet), container queries for cards/charts (`@container/card`, `@[540px]/card:block`).
- Mobile: header collapses to trigger + title + avatar; tables scroll horizontally (`overflow-x-auto`) or switch to `Item` lists; toggles become `Select`;
  dialogs become `Drawer` (`useIsMobile()`); tap targets ≥ 40px; no hover-only affordances.
- Test at 390 and 1440 (plus 1024 when tables are involved) — the mockup script does this by default.

## Accessibility

- Every Dialog/Sheet/Drawer has a Title; every icon-only button has `aria-label` or `sr-only` text; form controls are labeled via `FieldLabel htmlFor`.
- Focus: keep the shipped `focus-visible:ring-3 ring-ring/50`; never remove outlines without a replacement.
- Contrast: 4.5:1 for text, 3:1 for large text/UI; muted text that carries meaning must pass 4.5:1 (theme `check` tells you).
- Keyboard: ⌘/Ctrl+B sidebar, ⌘K command palette, Escape closes overlays (shipped); custom lists get roving focus or are rendered as `Command`.
- Charts: `accessibilityLayer`, textual summary in the card description, table/CSV alternative for critical data.

## Content

- Sentence case for titles, labels, buttons ("Save changes", not "Save Changes"); verbs on buttons; no "Submit".
- Numbers formatted with `Intl.NumberFormat` / `toLocaleString`; dates relative when < 7 days ("2 days ago") with the absolute date in a tooltip.
- Empty-state and error copy says what happened and what to do next, in one sentence each.

## Anti-patterns to catch in review

Raw palette colors or hex in components · `space-y-*` · `w-4 h-4` · custom divs where a component exists (Alert, Empty, Badge, Skeleton, Separator) ·
everything inside `CardContent` with no header · `Dialog` without a title · icon-only buttons without labels · hover-only actions on touch · charts without
height, tooltip or legend · dark mode via `dark:` overrides · two primary buttons in one view · lorem ipsum in the simulation · "done" without a mockup.
