# Charts: Recharts 3 + shadcn `chart`

Contents: [API](#api) · [Colors](#colors) · [Examples](#examples) · [Catalog](#catalog-70-installable-examples) ·
[Gotchas](#gotchas) · [Data-viz rules](#data-viz-rules)

`npx shadcn@latest add chart` → `components/ui/chart.tsx` (+ `recharts@3.8.0`, depends on `card`). shadcn does not wrap
Recharts: you compose Recharts components and add `ChartContainer`, `ChartTooltip(Content)`, `ChartLegend(Content)` where needed.
Browse: https://ui.shadcn.com/charts/{area,bar,line,pie,radar,radial,tooltip}. Docs: https://ui.shadcn.com/docs/components/chart.

## API

```tsx
import { ChartContainer, ChartTooltip, ChartTooltipContent, ChartLegend, ChartLegendContent, ChartStyle, type ChartConfig } from "@/components/ui/chart"

type ChartConfig = Record<string, { label?: React.ReactNode; icon?: React.ComponentType } &
  ({ color?: string; theme?: never } | { color?: never; theme: { light: string; dark: string } })>
```

- `ChartContainer` props: `config` (required), `className`, `children` (exactly one Recharts chart), `id?`,
  `initialDimension?` (default 320×200 for SSR). It renders `ResponsiveContainer` for you — never nest another one — and emits
  `--color-<key>` CSS variables from the config (`.dark` variants from `theme.dark`). Default class is `aspect-video`;
  **always give it a height**: `className="min-h-[200px] w-full"`, `"h-[250px] w-full"`, or `"aspect-auto h-[250px] w-full"`.
- `ChartTooltip` = Recharts `Tooltip` (so `cursor={false}`, `defaultIndex`, …). `ChartTooltipContent` props: `indicator="dot"|"line"|"dashed"`,
  `hideLabel`, `hideIndicator`, `labelKey`, `nameKey`, `labelFormatter(value, payload)`, `formatter(value, name, item, index, payload)`,
  `labelClassName`, `color`. Values render with `toLocaleString()` in `font-mono tabular-nums`.
- `ChartLegend` = Recharts `Legend`. `ChartLegendContent` props: `hideIcon`, `nameKey`, `verticalAlign="top"|"bottom"`.
- Key lookup: tooltip name = `nameKey ?? item.name ?? item.dataKey`; when the row holds a string at that key (e.g. `browser: "chrome"`) the config
  entry for that string is used — the `labelKey="visitors" nameKey="browser"` pattern for pie/bar-by-category.
- `accessibilityLayer` on the Recharts chart root adds keyboard + screen-reader support. Add it.

## Colors

```css
/* tokens (theme files already define these) */
:root { --chart-1: oklch(0.646 0.222 41.116); --chart-2: oklch(0.6 0.118 184.704); --chart-3: oklch(0.398 0.07 227.392); --chart-4: oklch(0.828 0.189 84.429); --chart-5: oklch(0.769 0.188 70.08); }
.dark { --chart-1: oklch(0.488 0.243 264.376); --chart-2: oklch(0.696 0.17 162.48); --chart-3: oklch(0.769 0.188 70.08); --chart-4: oklch(0.627 0.265 303.9); --chart-5: oklch(0.645 0.246 16.439); }
```

```tsx
const chartConfig = {
  desktop: { label: "Desktop", color: "var(--chart-1)" },           // recommended: theme token
  mobile:  { label: "Mobile",  theme: { light: "#2563eb", dark: "#60a5fa" } }, // or per-mode literals
  tablet:  { label: "Tablet",  color: "oklch(0.5 0.2 240)", icon: TabletIcon },
} satisfies ChartConfig

<Bar dataKey="desktop" fill="var(--color-desktop)" />                  // components
const data = [{ browser: "chrome", visitors: 275, fill: "var(--color-chrome)" }] // per-row fills (pie/radial)
<LabelList className="fill-(--color-desktop)" />                      // Tailwind v4 var syntax
```

Monochrome dashboards (dashboard-01) use `color: "var(--primary)"` for every series; multi-series use `--chart-1..5`
(the dotfile themes map five distinct accents to them). Use `var(--chart-1)`, never `hsl(var(--chart-1))` (Tailwind v3 era).

## Examples

**Area with gradient (`chart-area-gradient`)** — the pattern behind the dashboard chart:

```tsx
"use client"
import { Area, AreaChart, CartesianGrid, XAxis } from "recharts"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"

const chartData = [
  { month: "January", desktop: 186, mobile: 80 },
  { month: "February", desktop: 305, mobile: 200 },
  { month: "March", desktop: 237, mobile: 120 },
]
const chartConfig = {
  desktop: { label: "Desktop", color: "var(--chart-1)" },
  mobile: { label: "Mobile", color: "var(--chart-2)" },
} satisfies ChartConfig

export function ChartAreaGradient() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Visitors</CardTitle>
        <CardDescription>Last 6 months</CardDescription>
      </CardHeader>
      <CardContent>
        <ChartContainer config={chartConfig} className="min-h-[200px] w-full">
          <AreaChart accessibilityLayer data={chartData} margin={{ left: 12, right: 12 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="month" tickLine={false} axisLine={false} tickMargin={8} tickFormatter={(v) => v.slice(0, 3)} />
            <ChartTooltip cursor={false} content={<ChartTooltipContent />} />
            <defs>
              <linearGradient id="fillDesktop" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="var(--color-desktop)" stopOpacity={0.8} />
                <stop offset="95%" stopColor="var(--color-desktop)" stopOpacity={0.1} />
              </linearGradient>
              <linearGradient id="fillMobile" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="var(--color-mobile)" stopOpacity={0.8} />
                <stop offset="95%" stopColor="var(--color-mobile)" stopOpacity={0.1} />
              </linearGradient>
            </defs>
            <Area dataKey="mobile" type="natural" fill="url(#fillMobile)" fillOpacity={0.4} stroke="var(--color-mobile)" stackId="a" />
            <Area dataKey="desktop" type="natural" fill="url(#fillDesktop)" fillOpacity={0.4} stroke="var(--color-desktop)" stackId="a" />
          </AreaChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
```

**Interactive area with range selector** (dashboard-01 `chart-area-interactive.tsx`): same chart over daily data with
`CardAction` holding a `ToggleGroup` (≥768px) and a `Select` (mobile) bound to `timeRange` state ("90d" | "30d" | "7d");
`ChartContainer className="aspect-auto h-[250px] w-full"`; `XAxis minTickGap={32}` with a date `tickFormatter`;
`ChartTooltipContent labelFormatter={(v) => new Date(v).toLocaleDateString("en-US", { month: "short", day: "numeric" })} indicator="dot"`.
Base UI variant: `<ToggleGroup multiple={false} value={timeRange ? [timeRange] : []} onValueChange={(v) => setTimeRange(v[0] ?? "90d")}>`
and `<Select onValueChange={(v) => { if (v !== null) setTimeRange(v) }}>`. Install the standalone version with
`npx shadcn@latest add chart-area-interactive` (adds a legend, uses `--chart-1/2`).

**Bar (first chart in the docs)**: `BarChart accessibilityLayer data` > `CartesianGrid vertical={false}` + `XAxis` + `ChartTooltip` + `ChartLegend content={<ChartLegendContent />}` +
`<Bar dataKey="desktop" fill="var(--color-desktop)" radius={4} />`. **Pie/donut**: `Pie data dataKey="visitors" nameKey="browser" innerRadius={60}` with per-row `fill`;
center text via `Label content={({ viewBox }) => …}`. **Radial**: `RadialBarChart` + `PolarGrid`/`PolarRadiusAxis`. **Line**: `Line type="natural" dot={false}` or custom dots.

## Catalog (70 installable examples)

`npx shadcn@latest add <id>` → `components/<id>.tsx` (deps `card`, `chart`; `chart-area-interactive` also `select`).

- **area**: chart-area-interactive · chart-area-default · chart-area-linear · chart-area-step · chart-area-legend · chart-area-stacked · chart-area-stacked-expand · chart-area-icons · chart-area-gradient · chart-area-axes
- **bar**: chart-bar-interactive · chart-bar-default · chart-bar-horizontal · chart-bar-multiple · chart-bar-stacked · chart-bar-label · chart-bar-label-custom · chart-bar-mixed · chart-bar-active · chart-bar-negative
- **line**: chart-line-interactive · chart-line-default · chart-line-linear · chart-line-step · chart-line-multiple · chart-line-dots · chart-line-dots-custom · chart-line-dots-colors · chart-line-label · chart-line-label-custom
- **pie**: chart-pie-simple · chart-pie-separator-none · chart-pie-label · chart-pie-label-custom · chart-pie-label-list · chart-pie-legend · chart-pie-donut · chart-pie-donut-active · chart-pie-donut-text · chart-pie-stacked · chart-pie-interactive
- **radar**: chart-radar-default · chart-radar-dots · chart-radar-lines-only · chart-radar-label-custom · chart-radar-grid-custom · chart-radar-grid-none · chart-radar-grid-circle · chart-radar-grid-circle-no-lines · chart-radar-grid-circle-fill · chart-radar-grid-fill · chart-radar-multiple · chart-radar-legend · chart-radar-icons · chart-radar-radius
- **radial**: chart-radial-simple · chart-radial-label · chart-radial-grid · chart-radial-text · chart-radial-shape · chart-radial-stacked
- **tooltip**: chart-tooltip-default · chart-tooltip-indicator-line · chart-tooltip-indicator-none · chart-tooltip-label-custom · chart-tooltip-label-formatter · chart-tooltip-label-none · chart-tooltip-formatter · chart-tooltip-icons · chart-tooltip-advanced

## Gotchas

- Recharts 3 (since Mar 2026): `var(--chart-1)` not `hsl(var(--chart-1))`; `ChartTooltip.defaultIndex` only for initial state; no `layout` on `<Bar>`
  when `<BarChart layout>` is set; `ChartContainer` needs a height/aspect for first measure.
- `"use client"` on every chart file (Recharts touches the DOM); import charts into server pages.
- Chart keys must match `dataKey`s (`--color-<dataKey>`); for category charts put `fill: "var(--color-<rowValue>)"` on the data rows and key the config by row values.
- Tooltip cursor: `cursor={false}` on `ChartTooltip` for area/line; bar charts style the cursor via the container (`fill-muted`).
- SSR: `initialDimension` avoids a zero-size first render; hydration mismatches usually mean the chart rendered on the server with different data.
- Theming: the dotfile themes recolor charts automatically through `--chart-1..5`; shadcn/create presets can set a separate chart color; verify both modes in the mockup.

## Data-viz rules

(Keep these even when the operator just says "add a chart".)

- Pick the form from the question: trend → area/line; compare categories → bar (horizontal when labels are long); part-of-whole with ≤5 slices → donut, otherwise bar;
  distribution over two axes → radar only for ≤6 axes with the same scale; single KPI progress → radial.
- One message per chart: ≤4 series on one axis, direct labels or legend, title says what changed ("Visitors up 12% this month"), subtitle gives period.
- Axes: hide axis lines/ticks like the examples (`tickLine={false} axisLine={false}`), keep gridlines horizontal only, start bars at zero, format numbers with units
  (`tickFormatter`), dates short (`Apr 5`).
- Colors come from `--chart-n` in series order; use `--primary` for single-series emphasis; never encode meaning with color alone (add labels/icons);
  semantic good/bad use `--success`/`--destructive` tokens when the theme defines them.
- Interaction: tooltip always; range toggles for time series (90d/30d/7d); loading via `Skeleton` of the same height; empty via `Empty` inside the card.
- Accessibility: `accessibilityLayer`, a `CardTitle`/`CardDescription` that reads as a sentence, and a table alternative or CSV export for important data.
