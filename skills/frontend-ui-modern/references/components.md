# Components: catalog, composition, code rules

Contents: [Catalog](#catalog) · [Selection table](#selection-table) · [Composition trees](#composition-trees) ·
[Base UI vs Radix vs Aria](#base-ui-vs-radix-vs-aria) · [Code rules](#code-rules) · [States](#states)

Install: `npx shadcn@latest add <name> [<name>...]`. Docs per base: `https://ui.shadcn.com/docs/components/<base|radix|aria>/<name>`
(`npx shadcn@latest docs <name>` prints docs/examples/API URLs). Every page has a Composition tree; every page is also
available as Markdown by appending `.md`.

## Catalog (65 registry:ui items, Oct 2026)

**Actions & inputs** — `button` (variants default/outline/secondary/ghost/destructive/link; sizes xs/sm/default/lg/icon*) ·
`button-group` (Oct 2025; grouped buttons/inputs, `ButtonGroupSeparator`/`ButtonGroupText`) · `toggle` · `toggle-group` ·
`input` · `textarea` · `input-group` (Oct 2025; addons, inline buttons, helper text) · `input-otp` · `native-select` ·
`select` · `combobox` (autocomplete, chips/multi, groups) · `checkbox` · `radio-group` · `switch` · `slider` · `calendar` ·
`date-picker` (pattern: Calendar in Popover) · `field` (Oct 2025; Field/FieldGroup/FieldSet/FieldLabel/FieldDescription/FieldError/FieldSeparator) ·
`label` · `kbd` (Oct 2025).

**Layout & navigation** — `sidebar` (provider, variants sidebar/floating/inset, collapsible offcanvas/icon/none, cookie-persisted, ⌘/Ctrl+B) ·
`navigation-menu` (not in Aria) · `menubar` (not in Aria) · `breadcrumb` · `tabs` · `pagination` · `separator` · `resizable` ·
`scroll-area` · `aspect-ratio` · `collapsible` · `accordion` · `carousel` · `direction` (RTL provider).

**Content & data** — `card` (Header/Title/Description/Action/Content/Footer; `size="sm"`) · `table` · `data-table` (pattern: TanStack Table) ·
`badge` (default/secondary/outline/destructive/ghost/link) · `avatar` (+ AvatarFallback, AvatarBadge, group count) · `item` (Oct 2025; media/title/description/actions rows) ·
`empty` (Oct 2025) · `skeleton` · `progress` · `spinner` (Oct 2025) · `chart` (Recharts 3) · `code-block` (Oct 2026) · `typography` (docs-only styles) ·
Typeset (`/typeset`: one CSS file for rendered markdown; `.typeset .typeset-docs`).

**Overlays & feedback** — `dialog` · `alert-dialog` · `sheet` · `drawer` · `popover` · `hover-card` · `tooltip` · `dropdown-menu` · `context-menu` ·
`command` (⌘K palette, inside Dialog) · `alert` (default/destructive) · `toast` (Base UI only, Jul 2026) · `sonner` (Radix/Aria toast).

**Chat & agent UI (Jun–Oct 2026)** — `message-scroller` (anchored turns, streaming follow, history prepend, jump-to-latest) ·
`message` (row: avatar/header/content/footer) · `bubble` · `attachment` · `marker` (status notes, dividers) · `trace` (reasoning/tool/source steps) ·
`questionnaire` (multi-step clarifying questions) · utilities `scroll-fade`, `shimmer` · **AI Elements** = 21 copy-in patterns
(chat, prompt-input, reasoning, tool, plan, subagent, sources, terminal, test-results, …) at `/docs/ai-elements/<base>/<pattern>`.

Retired/renamed: `toast`→`sonner` for Radix/Aria (2025); `form` (react-hook-form wrapper) lives only in the legacy registry — use
`field` + `/docs/forms/{react-hook-form,tanstack-form,formisch}`; OTP and Calendar *blocks* were removed in Feb 2026 (the components remain).

## Selection table

| Need | Use |
| --- | --- |
| Primary / secondary / quiet / dangerous action | `Button` variant default / outline or secondary / ghost / destructive (tinted, not solid, in 2026 styles) |
| Form layout | `FieldGroup` > `Field` > `FieldLabel` + control + `FieldDescription` / `FieldError`; `FieldSet` + `FieldLegend` for related checkboxes/radios |
| Input with prefix/suffix/button | `InputGroup` + `InputGroupInput` + `InputGroupAddon` / `InputGroupButton` / `InputGroupText` |
| 2–7 exclusive choices | `ToggleGroup`; many choices → `Select`; searchable/large → `Combobox`; native feel → `NativeSelect` |
| KPI tiles | `Card size="sm"` with `CardDescription` (label), `CardTitle` (value, `tabular-nums`), `CardAction` (`Badge variant="outline"` trend) |
| Lists of things with media + actions | `ItemGroup` > `Item`; dense records → `Table` / data-table |
| App shell | `SidebarProvider` + `Sidebar` (+ `SidebarInset` for inset) + `SidebarTrigger` + `Breadcrumb` in a header |
| Side panel / confirm / modal / mobile sheet | `Sheet` / `AlertDialog` / `Dialog` / `Drawer` |
| Feedback | `toast` (Base) or `sonner` (Radix/Aria); inline → `Alert`; progress → `Progress`; loading → `Skeleton`, `Spinner` |
| Nothing here yet | `Empty` (icon, title, description, action) |
| Keyboard hints | `Kbd` |
| Charts | `chart` + Recharts (see charts.md) |
| Chat / agent UI | `MessageScroller`, `Message`, `Bubble`, `Attachment`, `Marker`, `Trace`, `CodeBlock`, `Questionnaire` |

## Composition trees

```text
Card                          Field (one control)            FieldGroup (a form)                 InputGroup
├── CardHeader                ├── FieldLabel                 ├── Field …                         ├── InputGroupInput | InputGroupTextarea
│   ├── CardTitle             ├── Input/Textarea/Select/…    ├── FieldSeparator                  ├── InputGroupAddon
│   ├── CardDescription       ├── FieldDescription           └── Field …                         ├── InputGroupButton
│   └── CardAction            └── FieldError                 (FieldSet > FieldLegend + FieldDescription + FieldGroup for groups)
├── CardContent
└── CardFooter

ItemGroup > Item              Empty                          Dialog / Sheet                      Tabs
  ├── ItemHeader              ├── EmptyHeader                ├── DialogTrigger                   ├── TabsList > TabsTrigger…
  ├── ItemMedia               │   ├── EmptyMedia             └── DialogContent                   └── TabsContent…
  ├── ItemContent             │   ├── EmptyTitle                 ├── DialogHeader
  │   ├── ItemTitle           │   └── EmptyDescription           │   ├── DialogTitle  (required, sr-only if hidden)
  │   └── ItemDescription     └── EmptyContent                   │   └── DialogDescription
  ├── ItemActions                                                └── DialogFooter
  └── ItemFooter

Select                        Table                                        ButtonGroup
├── SelectTrigger             ├── TableCaption                             ├── Button | Input
│   └── SelectValue           ├── TableHeader > TableRow > TableHead…      ├── ButtonGroupSeparator
└── SelectContent             ├── TableBody > TableRow > TableCell…        └── ButtonGroupText
    ├── SelectGroup           └── TableFooter
    │   ├── SelectLabel
    │   └── SelectItem…       (items always inside their Group: SelectGroup, DropdownMenuGroup, CommandGroup)
    └── SelectSeparator

SidebarProvider
├── Sidebar [side, variant=sidebar|floating|inset, collapsible=offcanvas|icon|none]
│   ├── SidebarHeader            (team switcher / logo)
│   ├── SidebarContent
│   │   └── SidebarGroup > SidebarGroupLabel + SidebarGroupAction + SidebarGroupContent
│   │       └── SidebarMenu > SidebarMenuItem > SidebarMenuButton [isActive, tooltip] + SidebarMenuAction + SidebarMenuBadge
│   │                                            └── SidebarMenuSub > SidebarMenuSubItem > SidebarMenuSubButton
│   ├── SidebarFooter            (user menu)
│   └── SidebarRail
├── SidebarInset                 (main; use with variant="inset")
└── SidebarTrigger               (in the header; keyboard ⌘/Ctrl+B)
```

Sidebar essentials: `useSidebar()` → `{ state, open, setOpen, openMobile, setOpenMobile, isMobile, toggleSidebar }`;
widths via provider style `--sidebar-width` (16rem) / `--sidebar-width-icon` (3rem); mobile (<768px) renders inside a Sheet;
state persisted in the `sidebar_state` cookie (read it server-side and pass `defaultOpen`).

## Base UI vs Radix vs Aria

Check `base` from `npx shadcn@latest info` before writing JSX.

| Topic | Base UI (`base`, default) | Radix (`radix`) |
| --- | --- | --- |
| Custom trigger element | `<DialogTrigger render={<Button />}>Open</DialogTrigger>` | `<DialogTrigger asChild><Button>Open</Button></DialogTrigger>` |
| Button as link | `<Button render={<a href="/x" />} nativeButton={false}>` (or `buttonVariants()` on `<a>`) | `<Button asChild><a href="/x">…</a></Button>` |
| Select | root needs `items` prop; placeholder = item `{ value: null }`; `alignItemWithTrigger`; supports `multiple` & object values; `onValueChange` may pass `null` | inline JSX items; `position="popper"` |
| ToggleGroup | `multiple` boolean, array values (`value={[v]}`) | `type="single" \| "multiple"` |
| Slider | scalar value for one thumb | array value |
| Accordion | no `type`; `multiple` + array `defaultValue` | `type="single" \| "multiple"` |
| Toast | `toast` component (`import { Toaster, toast } from "@/components/ui/toast"`) | `sonner` |
| Menubar / NavigationMenu | available | available (React Aria: not available) |

Applies to all triggers/closes: Dialog, Sheet, AlertDialog, DropdownMenu, Popover, Tooltip, Collapsible, DialogClose, SheetClose,
NavigationMenuLink, BreadcrumbLink, SidebarMenuButton, Badge, Item. Blocks install in the right variant automatically.

## Code rules

(Condensed from shadcn's own skill; the full Incorrect/Correct pairs are in `skills/shadcn/rules/*.md` of the shadcn repo.)

- **Semantic tokens only**: `bg-background`, `text-muted-foreground`, `border-border`, `bg-primary text-primary-foreground`.
  Never `bg-zinc-900`, `text-emerald-600`, hex, or `dark:` color overrides — themes must switch without touching components.
  Status colors: `Badge` variants or the extra tokens (`bg-success/10 text-success`) when the theme defines them.
- **`className` is for layout** (width, grid, gap, alignment), **variants are for looks**. Don't override component colors/typography.
- **Spacing**: `flex flex-col gap-4`, never `space-y-*`/`space-x-*`. Squares: `size-4`, not `w-4 h-4`. Truncation: `truncate`.
- **Composition**: items inside their Group; full Card composition (no everything-in-CardContent); `TabsTrigger` inside `TabsList`;
  `Avatar` always with `AvatarFallback`; Dialog/Sheet/Drawer always with a Title (`sr-only` if hidden); no manual `z-index` on overlays.
- **Forms**: `FieldGroup` + `Field`; `data-invalid` on `Field` and `aria-invalid` on the control; `data-disabled` on `Field`, `disabled` on the control;
  buttons inside inputs via `InputGroup`; option sets via `ToggleGroup`; checkbox/radio groups via `FieldSet` + `FieldLegend`.
  React Hook Form / TanStack Form / Formisch recipes: `/docs/forms/*`.
- **Icons**: inside `Button`, `<Icon data-icon="inline-start" />` / `inline-end`, no size classes; pass icon components (`icon={CheckIcon}`), not strings;
  import from the project's `iconLibrary` (`lucide-react`, `@tabler/icons-react`, `@hugeicons/react`, `@phosphor-icons/react`, `@remixicon/react`).
- **Loading button**: compose `Spinner` + `disabled` (`Button` has no `isLoading`).
- **`cn()`** for conditional classes (`import { cn } from "@/lib/utils"`).
- **Client boundaries** (`rsc: true`): anything with state, effects, handlers, Recharts, or the sidebar needs `"use client"`; pages stay server components.
- **Existing first**: search registries (`npx shadcn@latest search @shadcn -q "…"`) before custom markup; `Alert` for callouts, `Empty` for empty states,
  `Separator` not `<hr>`, `Skeleton` not `animate-pulse` divs, `Badge` not styled spans.
- **Third-party registry items**: read every added file; fix alias paths and icon imports; verify composition.

## States

Every screen in scope shows, where applicable: **loading** (`Skeleton` shaped like the content; `Spinner` in buttons),
**empty** (`Empty` with one primary action), **error** (`Alert variant="destructive"` for page-level, `FieldError` for fields,
toast for transient failures), **success/feedback** (toast; inline `Alert`), **long content** (`truncate`, `line-clamp-*`, wrapping tables → `overflow-x-auto`),
**permissions** (disabled with tooltip explaining why), **keyboard** (focus-visible rings — never `outline-none` without replacement).
