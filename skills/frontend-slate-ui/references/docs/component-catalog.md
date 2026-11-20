# SlateUI Component Directive Catalog (27 Directives)

Extracted verbatim from `github.com/angularcafe/slateui@84d0884` (`projects/ui/src/directives/`). Recommended install path: copy the needed `.ts` files offline from `references/components/` into `ui/directives/` (using path alias `@workspace/ui/*`), because `@slateui/cli:add` fetches unpinned `main` over GitHub and accepts only one `itemName` per invocation.

| Directive File | Selectors | Exported Classes | Angular Primitives (`ng-primitives`) | Offline Copy / Single-Item CLI |
|---|---|---|---|---|
| `components/accordion.ts` | `[uiAccordion]`, `[uiAccordionTrigger]`, `[uiAccordionItem]`, `[uiAccordionContent]` | `UiAccordion`, `UiAccordionTrigger`, `UiAccordionItem`, `UiAccordionContent` | `ng-primitives/accordion` | `ng g @slateui/cli:add accordion` |
| `components/alert-dialog.ts` | `ui-alert-dialog`, `ui-alert-dialog-header`, `ui-alert-dialog-title`, `ui-alert-dialog-description`, `ui-alert-dialog-footer`, `[uiAlertDialogTrigger]`, `ui-alert-dialog-overlay`, `ui-alert-dialog-cancel`, `ui-alert-dialog-action` | `UiAlertDialog`, `UiAlertDialogHeader`, `UiAlertDialogTitle`, `UiAlertDialogDescription`, `UiAlertDialogFooter`, `UiAlertDialogTrigger`, `UiAlertDialogOverlay`, `UiAlertDialogCancel`, `UiAlertDialogAction` | `ng-primitives/dialog` | `ng g @slateui/cli:add alert-dialog` |
| `components/alert.ts` | `ui-alert`, `ui-alert-title`, `ui-alert-description` | `UiAlert`, `UiAlertTitle`, `UiAlertDescription` | — | `ng g @slateui/cli:add alert` |
| `components/avatar.ts` | `ui-avatar`, `ui-avatar-image`, `ui-avatar-fallback` | `UiAvatar`, `UiAvatarImage`, `UiAvatarFallback` | `ng-primitives/avatar` | `ng g @slateui/cli:add avatar` |
| `components/badge.ts` | `ui-badge` | `UiBadge` | — | `ng g @slateui/cli:add badge` |
| `components/breadcrumb.ts` | `ui-breadcrumb`, `ui-breadcrumb-list`, `ui-breadcrumb-item`, `ui-breadcrumb-link`, `ui-breadcrumb-page`, `ui-breadcrumb-separator`, `ui-breadcrumb-ellipsis` | `UiBreadcrumb`, `UiBreadcrumbList`, `UiBreadcrumbItem`, `UiBreadcrumbLink`, `UiBreadcrumbPage`, `UiBreadcrumbSeparator`, `UiBreadcrumbEllipsis` | — | `ng g @slateui/cli:add breadcrumb` |
| `components/button.ts` | `[uiButton]` | `UiButton` | `ng-primitives/button` | `ng g @slateui/cli:add button` |
| `components/card.ts` | `ui-card`, `ui-card-header`, `ui-card-title`, `ui-card-description`, `ui-card-action`, `ui-card-content`, `ui-card-footer` | `UiCard`, `UiCardHeader`, `UiCardTitle`, `UiCardDescription`, `UiCardAction`, `UiCardContent`, `UiCardFooter` | — | `ng g @slateui/cli:add card` |
| `components/checkbox.ts` | `ui-checkbox` | `UiCheckbox` | `ng-primitives/checkbox` | `ng g @slateui/cli:add checkbox` |
| `components/dialog.ts` | `[uiDialog]`, `[uiDialogHeader]`, `[uiDialogTitle]`, `[uiDialogDescription]`, `[uiDialogFooter]`, `[uiDialogTrigger]`, `[uiDialogOverlay]` | `UiDialog`, `UiDialogHeader`, `UiDialogTitle`, `UiDialogDescription`, `UiDialogFooter`, `UiDialogTrigger`, `UiDialogOverlay` | `ng-primitives/dialog` | `ng g @slateui/cli:add dialog` |
| `components/dropdown-menu.ts` | `[uiDropdownMenu]`, `[uiDropdownMenuTrigger]`, `[uiDropdownMenuItem]`, `[uiDropdownMenuShortcut]`, `[uiDropdownMenuSeparator]`, `[uiDropdownMenuLabel]`, `[uiDropdownSubmenuTrigger]` | `UiDropdownMenu`, `UiDropdownMenuTrigger`, `UiDropdownMenuItem`, `UiDropdownMenuShortcut`, `UiDropdownMenuSeparator`, `UiDropdownMenuLabel`, `UiDropdownSubmenuTrigger` | `ng-primitives/menu`, `ng-primitives/separator` | `ng g @slateui/cli:add dropdown-menu` |
| `components/form-field.ts` | `[uiFormField]`, `[uiLabel]`, `[uiDescription]`, `[uiError]` | `UiFormField`, `UiLabel`, `UiDescription`, `UiError` | `ng-primitives/form-field` | `ng g @slateui/cli:add form-field` |
| `components/input.ts` | `input[uiInput]` | `UiInput` | `ng-primitives/input` | `ng g @slateui/cli:add input` |
| `components/label.ts` | `[uiLabel]` | `UiLabel` | `ng-primitives/form-field` | `ng g @slateui/cli:add label` |
| `components/popover.ts` | `[uiPopover]`, `[uiPopoverTrigger]` | `UiPopover`, `UiPopoverTrigger` | `ng-primitives/popover` | `ng g @slateui/cli:add popover` |
| `components/progress.ts` | `[uiProgress]`, `[uiProgressIndicator]` | `UiProgress`, `UiProgressIndicator` | `ng-primitives/progress` | `ng g @slateui/cli:add progress` |
| `components/radio-group.ts` | `[uiRadioGroup]`, `[uiRadioItem]`, `[uiRadio]`, `[uiRadioIndicator]` | `UiRadioGroup`, `UiRadioItem`, `UiRadio`, `UiRadioIndicator` | `ng-primitives/radio` | Copy-only from `references/components/radio-group.ts` (not in upstream `registry.json`) |
| `components/separator.ts` | `[uiSeparator]` | `UiSeparator` | `ng-primitives/separator` | `ng g @slateui/cli:add separator` |
| `components/sheet.ts` | `[uiSheet]`, `[uiSheetHeader]`, `[uiSheetTitle]`, `[uiSheetDescription]`, `[uiSheetFooter]`, `[uiSheetTrigger]`, `[uiSheetOverlay]` | `UiSheet`, `UiSheetHeader`, `UiSheetTitle`, `UiSheetDescription`, `UiSheetFooter`, `UiSheetTrigger`, `UiSheetOverlay` | `ng-primitives/dialog` | `ng g @slateui/cli:add sheet` |
| `components/skeleton.ts` | `[uiSkeleton]` | `UiSkeleton` | — | `ng g @slateui/cli:add skeleton` |
| `components/switch.ts` | `[uiSwitch]`, `[uiSwitchThumb]` | `UiSwitch`, `UiSwitchThumb` | `ng-primitives/switch` | `ng g @slateui/cli:add switch` |
| `components/table.ts` | `[uiTable]`, `[uiTableHeader]`, `[uiTableBody]`, `[uiTableFooter]`, `[uiTableRow]`, `[uiTableHead]`, `[uiTableCell]`, `[uiTableCaption]` | `UiTable`, `UiTableHeader`, `UiTableBody`, `UiTableFooter`, `UiTableRow`, `UiTableHead`, `UiTableCell`, `UiTableCaption` | — | `ng g @slateui/cli:add table` |
| `components/tabs.ts` | `[uiTabs]`, `[uiTabsContent]`, `[uiTabsList]`, `[uiTabsTrigger]` | `UiTabs`, `UiTabsContent`, `UiTabsList`, `UiTabsTrigger` | `ng-primitives/tabs` | `ng g @slateui/cli:add tabs` |
| `components/textarea.ts` | `textarea[uiTextarea]` | `UiTextarea` | `ng-primitives/input` | `ng g @slateui/cli:add textarea` |
| `components/toggle-group.ts` | `[uiToggleGroup]`, `[uiToggleGroupItem]` | `UiToggleGroup`, `UiToggleGroupItem` | `ng-primitives/toggle-group` | `ng g @slateui/cli:add toggle-group` |
| `components/toggle.ts` | `[uiToggle]` | `UiToggle` | `ng-primitives/button`, `ng-primitives/toggle` | `ng g @slateui/cli:add toggle` |
| `components/tooltip.ts` | `[uiTooltip]`, `[uiTooltipTrigger]`, `[uiTooltipArrow]` | `UiTooltip`, `UiTooltipTrigger`, `UiTooltipArrow` | `ng-primitives/tooltip` | `ng g @slateui/cli:add tooltip` |
