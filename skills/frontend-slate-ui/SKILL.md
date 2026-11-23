---
name: frontend-slate-ui
description: "Angular 20 components with SlateUI directives, ng-primitives, Signals, utility tokens, and vendored ThemeService."
---

# frontend-slate-ui — Angular 20 UI with SlateUI (`slateui.dev`)

SlateUI (`github.com/angularcafe/slateui`) is an MIT-licensed Angular directive component library: lightweight, tree-shakable, standalone **directive-based components** built on **Angular 20 Signals**, **Angular Primitives (`ng-primitives@0.80.0`)**, **Tailwind CSS 4**, and **`tailwind-variants@1.0.0`**, paired with a lightweight Signal-driven `ThemeService` (`github.com/angularcafe/slateui-theme`).

> **Upstream pin & compatibility note:** Upstream `@slateui/cli@0.2.0` (`^20.1.0`) and `@slateui/theme@1.0.0` (`^19 || ^20.1`, `slateui-theme@1e01611`) cap at **Angular 20** (last released August 2025; `angularcafe/slateui@84d0884` last moved 2025-10-23; Angular 20 LTS ends `2026-11-28`, whereas `ng-primitives@0.80.0` peers on `@angular/* >= 19`), and `@slateui/cli` pulls unpinned `main` / Angular 21+ peers at runtime. Therefore, **the supported offline workflow copies the vendored directives (`references/components/*.ts` → `ui/directives/` + `ui/public-api.ts`) and vendored theme service (`references/theme-pkg/projects/theme/src/lib/*.ts` → `src/app/theme/`) directly into your Angular workspace** — zero runtime dependency on `@slateui/cli` or `@slateui/theme`, making Angular 22 upgrades strictly local to owned files.

## Bundled References & Assets (Pinned Upstream Snapshots)

- Upstream commits mirrored under `references/` and `assets/`:
  - `github.com/angularcafe/slateui` @ **`84d0884`** (27 directives, docs, `registry.json`, CSS presets, brand SVG and syntax-theme assets)
  - `github.com/angularcafe/slateui-theme` @ **`1e01611`** (`projects/theme/src/lib/` Signal `ThemeService`)

| Path | Contents |
|---|---|
| [references/docs/component-catalog.md](references/docs/component-catalog.md) | Complete index of all 27 directives, exact selectors (`[uiButton]`, `ui-card`, `[uiSwitch]`, `[uiTabs]`, `ui-alert-dialog`, etc.), exported classes, and `ng-primitives` peer imports (`radio-group` is copy-only) |
| [references/registry.json](references/registry.json) | Upstream registry manifest (26 CLI items; `radio-group` is in [references/components/radio-group.ts](references/components/radio-group.ts) only) |
| `references/components/*.ts` | Full verbatim TypeScript source of all 27 directives (`accordion`, `alert-dialog`, `alert`, `avatar`, `badge`, `breadcrumb`, `button`, `card`, `checkbox`, `dialog`, `dropdown-menu`, `form-field`, `input`, `label`, `popover`, `progress`, `radio-group`, `separator`, `sheet`, `skeleton`, `switch`, `table`, `tabs`, `textarea`, `toggle-group`, `toggle`, `tooltip`) + `public-api.ts` |
| `references/docs/pages/` | Complete `slateui.dev` docs (`introduction`, `installation`, `cli`, `components-json`, `theming`, `dark-mode`, and all 26 component usage/example pages) |
| `references/themes/` | Upstream `styles.constants.ts` (`neutral`, `slate`, `zinc`, `stone`, `gray`), `docs-styles.css`, `tw-animate.css`, and WCAG AA-verified [antigravity-slate.css](references/themes/antigravity-slate.css) (Dark Obsidian + Crisp Light) |
| [references/LICENSE](references/LICENSE), [references/README.md](references/README.md), [references/CONTRIBUTING.md](references/CONTRIBUTING.md), [references/package.json](references/package.json) | Upstream `slateui` MIT licence, overview, contributor guide, and peer package metadata |
| `references/theme-pkg/projects/theme/src/lib/*.ts`, [public-api.ts](references/theme-pkg/projects/theme/src/public-api.ts) & [LICENSE](references/theme-pkg/LICENSE) | Full verbatim `ThemeService` + `provideSlateUiTheme` source and MIT licence (copy into `src/app/theme/` instead of installing `@slateui/theme` from npm) |
| `assets/public/assets/slateui-logo*.svg` & `assets/highlight-themes/*.css` | Official SlateUI brand SVGs (`slateui-logo.svg`, `slateui-logo-dark.svg`) and `github.css` / `github-dark.css` syntax themes |

---

## Workflow (Pinned Offline-Vendored Path — Zero `@slateui/cli` Runtime Fetches)

### 1. Scaffold Angular 20.3 & Install Exact Compatible Dependencies

```bash
# 1. Create standalone Angular 20.3 workspace (non-interactive; --skip-install avoids ^20.0.0 peer ERESOLVE)
NG_CLI_ANALYTICS=false npx --yes @angular/cli@20.3.43 new web --style=css --routing --ssr=false --defaults --skip-install
cd web

# 2. Install pinned Angular 20.3 + SlateUI peer dependencies (no @slateui/cli or @slateui/theme npm tie)
npm install \
  @angular/common@20.3.35 @angular/compiler@20.3.35 @angular/core@20.3.35 \
  @angular/forms@20.3.35 @angular/platform-browser@20.3.35 @angular/router@20.3.35 \
  @angular/cdk@20.2.14 ng-primitives@0.80.0 \
  @ng-icons/core@32.5.0 @ng-icons/lucide@32.5.0 \
  tailwind-variants@1.0.0 tailwind-merge@3 tw-animate-css@1 @floating-ui/dom@1 \
  tailwindcss@4 @tailwindcss/postcss@4 postcss@8

npm install -D @angular/build@20.3.43 @angular/cli@20.3.43 @angular/compiler-cli@20.3.35

# 3. Wire Tailwind CSS 4 via .postcssrc.json (required so @import "tailwindcss" and @apply compile)
cat > .postcssrc.json <<'EOF'
{
  "plugins": {
    "@tailwindcss/postcss": {}
  }
}
EOF

# 4. Copy vendored SlateUI directives & ThemeService offline into your workspace (set SKILL_DIR to this skill folder)
mkdir -p ui/directives src/app/theme
cp "$SKILL_DIR"/references/components/*.ts ui/directives/
mv ui/directives/public-api.ts ui/public-api.ts
cp "$SKILL_DIR"/references/theme-pkg/projects/theme/src/lib/*.ts src/app/theme/
```

Add `"baseUrl": "."` and `"paths": { "@workspace/ui/*": ["./ui/*"] }` under `compilerOptions` in `tsconfig.json`.

> **Required owned-copy contrast fix (`ui/directives/button.ts`):** Upstream `references/components/button.ts` (line 10) hardcodes `bg-destructive text-white` (which renders at `2.77:1` against light-red dark-mode destructive backgrounds like `#f87171`). After copying into your project's owned `ui/directives/button.ts`, change `bg-destructive text-white` to `bg-destructive text-destructive-foreground` (`7.02:1` dark, `4.83:1` light). Keep `references/components/button.ts` verbatim; patch only your project's copied file.

### 2. Wire Vendored `ThemeService` and Tailwind CSS 4 Tokens (Zero Dark FOUC)

1. For dark-first apps, add `class="dark"` directly to `<html>` in `src/index.html` (`<html lang="en" class="dark">`) so first paint is dark before Angular's initializer runs.
2. Register `provideSlateUiTheme()` from your copied `./theme` module in `src/app/app.config.ts` with **`enableSystem: false`** (otherwise `ThemeService.toggle()` cycles `light → dark → system`, causing every third click to look dead on a dark OS):
   ```typescript
   import { ApplicationConfig } from '@angular/core';
   import { provideRouter } from '@angular/router';
   import { provideSlateUiTheme } from './theme';
   import { routes } from './app.routes';

   export const appConfig: ApplicationConfig = {
     providers: [
       provideRouter(routes),
       provideSlateUiTheme({
         strategy: 'class',
         defaultTheme: 'dark',
         storageKey: 'app-theme', // customize per application
         enableSystem: false,
       }),
     ],
   };
   ```
3. Copy [references/themes/antigravity-slate.css](references/themes/antigravity-slate.css) (dark-first Obsidian `#0c0d10` + Crisp Light `#ffffff`, text `4.83:1..17.85:1`, borders `1.36:1..1.48:1` including `#2b3040` on `--card`) or one of the five upstream presets in `references/themes/styles.constants.ts` (`slate`, `neutral`, `zinc`, `stone`, `gray`) into `src/styles.css`.
4. Toggle theme anywhere with Angular Signals via `ThemeService`:
   ```typescript
   private readonly theme = inject(ThemeService);
   // theme.isDark(), theme.resolvedTheme(), theme.setTheme('dark' | 'light'), theme.toggle()
   ```

### 3. Directive Composition Rules (Read Before Writing Templates)

- **Exact Selectors Matter:** Check [references/docs/component-catalog.md](references/docs/component-catalog.md) before writing markup. SlateUI mixes attribute directives (`button[uiButton]`, `input[uiInput]`, `textarea[uiTextarea]`, `[uiSwitch]`, `[uiTabs]`, `[uiDialog]`, `[uiSheet]`, `[uiTable]`, `[uiDropdownMenu]`) with element directives (`ui-card`, `ui-card-header`, `ui-card-title`, `ui-card-description`, `ui-card-action`, `ui-card-content`, `ui-card-footer`, `ui-badge`, `ui-avatar`, `ui-alert`, `ui-alert-dialog`, `ui-breadcrumb`, `ui-checkbox`).
- **Semantic Tokens Only:** Always use `bg-background`, `text-foreground`, `bg-card`, `text-muted-foreground`, `border-border`, `bg-primary text-primary-foreground`, `bg-destructive text-destructive-foreground`, `bg-sidebar`, `border-sidebar-border`. Avoid raw hex values or ad-hoc `dark:bg-zinc-900` overrides in component templates.
- **Signals & Standalone First:** Use `input()`, `output()`, `computed()`, and `signal()` for component state; import only the exact SlateUI directives used in each standalone component's `imports: [...]`.
- **Example Workspace Pattern (Single-App Dual-Mode Workspace ↔ Control Panel):**
  - Keep **one** Angular application with a top-left segmented mode switch (`aria-pressed` toggle buttons + keyboard shortcut `g` ignoring `Ctrl/Meta/Alt` and open overlays, plus `⌘K` command palette).
  - Track `activeMode = signal<'workspace' | 'control'>('workspace')`, `lastWorkspaceRoute`, and `lastControlRoute` so flipping between daily workspace and full-view settings/fleet cards preserves context with zero page reload.
  - Whenever an overlay/dialog opens, rely on `ng-primitives/dialog` focus trap (`aria-modal="true"`, `inert` background, `Escape` closing even from focused inputs, background shortcuts suppressed, focus restored to the opening trigger).

### 4. Verification & Proof Gate

Before calling any SlateUI build done:

1. **Build, Typecheck & Compiled Tailwind CSS Check:** Run `npm run build` (Angular AOT + strict template checks pass with 0 errors) and verify compiled `dist/*/browser/styles-*.css` contains **zero** raw `@apply` or `@theme` directives (proving `.postcssrc.json` processed Tailwind CSS 4).
2. **Playwright Visual + Accessibility + Rendered-Contrast Sweep:** Capture `@2x` screenshots in **both Dark and Light** at **1280px desktop and 390px mobile**:
   - 0 console errors and 0 uncaught page errors.
   - 0 horizontal overflows (`scrollWidth <= clientWidth + 1`) at 390px and 1280px.
   - Mode switches update `aria-pressed` and preserve keyboard focus; dialogs set `aria-modal="true"` with `inert` background; `Escape` closes dialogs even when focus is inside an `<input>` or `<textarea>` and restores focus to the opener; single-key shortcuts ignore `Ctrl`/`Meta`/`Alt` and open overlays.
   - Rendered text contrast >= `4.5:1` on real mounted components (specifically including `[uiButton variant="destructive"]`, muted descriptions, and table headers), borders >= `1.3:1` on both `--background` and `--card`, `tabular-nums` on metrics/counts, and explicit loading (`[uiSkeleton]`), empty, and error (`ui-alert`) states.
