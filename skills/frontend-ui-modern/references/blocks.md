# Blocks: catalog and page recipes

Blocks are full pages (`registry:page` + components) installed with `npx shadcn@latest add <name>`; the CLI pulls the
variant matching your `components.json` base (Base UI uses `render`, Radix `asChild`). Browse: https://ui.shadcn.com/blocks
(tabs Featured / Sidebar / Login / Signup; featured = dashboard-01, sidebar-07, sidebar-03, login-03, login-04).
Pages land at `app/dashboard/page.tsx`, `app/login/page.tsx`, `app/signup/page.tsx` (rewritten per framework:
`pages/…`, `app/routes/…`, `resources/js/pages/…`); block components land in `components/`.
OTP (`otp-0N`) and Calendar (`calendar-NN`) blocks were removed in Feb 2026 — don't reference them.

## Catalog (28 public blocks, verified 2026-10-30)

| Block | What it is | Composes |
| --- | --- | --- |
| `dashboard-01` | Dashboard: inset sidebar, site header, 4 KPI cards, interactive stacked area chart (range toggle), TanStack data table with drag-sort, tabs and row drawer | sidebar, breadcrumb, separator, chart, card, select, tabs, table, toggle-group, badge, button, checkbox, dropdown-menu, drawer, input, avatar, sheet, sonner (+ `@dnd-kit/*`, `@tanstack/react-table`, `zod`); files: app-sidebar, chart-area-interactive, data-table, nav-documents, nav-main, nav-secondary, nav-user, section-cards, site-header, `data.json` |
| `sidebar-01` | Simple sidebar, navigation grouped by section, version switcher + search | sidebar, breadcrumb, separator, label, dropdown-menu |
| `sidebar-02` | Collapsible sections | same as 01 |
| `sidebar-03` | Submenus | sidebar, breadcrumb |
| `sidebar-04` | Floating sidebar with submenus (`variant="floating"`) | sidebar, breadcrumb, separator |
| `sidebar-05` | Collapsible submenus + search | sidebar, breadcrumb, separator, label, collapsible |
| `sidebar-06` | Submenus as dropdowns + opt-in form card | sidebar, breadcrumb, separator, card, dropdown-menu |
| `sidebar-07` | Collapses to icons (`collapsible="icon"`): team switcher, nav-main, projects, user menu — the most reused shell | sidebar, breadcrumb, separator, collapsible, dropdown-menu, avatar |
| `sidebar-08` | Inset sidebar with secondary navigation (`variant="inset"`) | sidebar, breadcrumb, separator, collapsible, dropdown-menu, avatar |
| `sidebar-09` | Collapsible nested sidebars (icon rail + inner panel) | sidebar, breadcrumb, separator, collapsible, dropdown-menu, avatar, switch, label |
| `sidebar-10` | Sidebar in a popover (workspaces, favorites, actions) | sidebar, breadcrumb, separator, popover, collapsible, dropdown-menu |
| `sidebar-11` | Collapsible file tree | sidebar, breadcrumb, separator, collapsible |
| `sidebar-12` | Sidebar with a calendar + date picker | sidebar, breadcrumb, separator, collapsible, calendar, dropdown-menu, avatar |
| `sidebar-13` | Sidebar inside a settings dialog (`collapsible="none"`) | sidebar, breadcrumb, button, dialog |
| `sidebar-14` | Sidebar on the right (`side="right"`) | sidebar, breadcrumb |
| `sidebar-15` | Left and right sidebars | sidebar, breadcrumb, separator, popover, collapsible, dropdown-menu, calendar, avatar |
| `sidebar-16` | Sidebar with a sticky site header | sidebar, breadcrumb, separator, collapsible, dropdown-menu, avatar, button, label |
| `login-01` | Centered login card | button, card, input, label, field |
| `login-02` | Two-column login with cover image | button, input, label, field |
| `login-03` | Login on muted background with logo | button, card, input, label, field |
| `login-04` | Wider card with form + image | button, card, input, label, field |
| `login-05` | Email-only login | button, input, label, field |
| `signup-01` | Simple signup form | button, card, input, label |
| `signup-02` | Two-column signup with cover image | button, input, label, field |
| `signup-03` | Signup on muted background | button, card, input, label, field |
| `signup-04` | Signup with form + image | button, card, input, label, field |
| `signup-05` | Signup with social providers | button, input, label |

Internal demo collages `preview`, `preview-02`, `preview-03` exist for shadcn/create — not for installing.
Chart examples (`chart-*`, 70 of them) are `registry:block` items too; see charts.md.

## Recipes (what to install for common asks)

| Ask | Install | Then |
| --- | --- | --- |
| Admin / analytics dashboard | `dashboard-01` (or `sidebar-07` + `chart-area-interactive` + `card` + `table`) | Replace `data.json`/nav with real data; keep `SectionCards` pattern for KPIs; one `ChartConfig` per chart |
| App shell for a product | `sidebar-07` (icon-collapsible) or `sidebar-08` (inset) or `sidebar-16` (sticky header) | Put `SidebarTrigger` + `Breadcrumb` in the header; persist state via the cookie |
| Settings page | `sidebar-13` (dialog) or Tabs + Cards + `field` | Sections as Cards; `FieldGroup` per section; danger zone as the last card |
| Auth | `login-04` + `signup-04` (image) or `login-01` + `signup-01` (minimal); `input-otp` for codes | Wire the form library from `/docs/forms`; add `Alert` for auth errors |
| Data-heavy list | `table` + data-table pattern (TanStack) from `dashboard-01`'s `data-table.tsx` | Column visibility via `DropdownMenu`, row actions via `DropdownMenu`, bulk via `Checkbox`, pagination via `Pagination` |
| Marketing/landing sections | not in @shadcn — search community registries (`npx shadcn@latest search @tailark -q hero`) and ask the operator which registry to use | Review imports/icons after install |
| Chat / agent product | `message-scroller message bubble attachment marker trace code-block` + AI Elements patterns | Follow `/docs/ai-elements/<base>/chat.md` |
| Command palette | `command` + `dialog` | ⌘K shortcut; `Kbd` hints |
| Docs/content pages | Typeset (`/typeset`) + `typography` | `.typeset .typeset-docs` wrapper around rendered markdown |

Assembly rules: pages are server components that import client blocks; keep block components in `components/` as installed
(rename only with a reason); set sidebar widths on `SidebarProvider` (`--sidebar-width: calc(var(--spacing) * 72)` in dashboard-01);
use container queries (`@container/main`, `@xl/main:grid-cols-2`, `@5xl/main:grid-cols-4`) for responsive card grids as the blocks do.
