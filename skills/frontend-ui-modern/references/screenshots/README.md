# Reference screenshots — proof of what the simulation kit renders

131 captures from `assets/simulation-template.html` via `scripts/mockup.mjs` (Chromium, 1× scale, quantized PNG), 2026-10-30.
Views modeled on shadcn's own set: dashboard-01 (KPIs + interactive area chart + data table with tabs), the Tasks example
(search, faceted filters, view options, pagination, row actions), login-04 / signup-03 / OTP, settings tabs (General, Members, Billing,
Notifications, API keys), a customer detail page, onboarding, 404; overlays (⌘K command, dialog, alert dialog, sheet, toasts, mobile nav);
states (loading skeletons, empty, error).

How to use them:

1. **While deciding** — show the operator the view that matches what they're asking for, in a few themes, before building the project-specific simulation.
2. **While building (proof)** — after each milestone, capture your build (`ui-mockups/<nn>-<milestone>/`) and compare with the matching reference:
   `theme-<id>-<mode>-1280.png` for colors (surface tiers, primary, chart palette, muted text) and `view-default-<view>-<mode>-1280.png` for
   structure (header, toolbar, table, pagination, states). A mismatch means the tokens or the composition didn't land.

Regenerate or extend: `node scripts/mockup.mjs ui-sim/index.html --views customers,customer --states normal,empty --overlays none,sheet --modes light,dark --full`.
File naming: `<kind>-<theme>-<view>[-<state>|-<overlay>]-<mode>-<width>.png`; `gallery.html` shows everything side by side.

## Themes — Dashboard view, every theme, light + dark (1280, viewport)

- `theme-ayu-mirage-overview-dark-1280.png` — Ayu (Light / Mirage) · dark · 1280px
- `theme-ayu-mirage-overview-light-1280.png` — Ayu (Light / Mirage) · light · 1280px
- `theme-ayu-overview-dark-1280.png` — Ayu (Light / Dark) · dark · 1280px
- `theme-ayu-overview-light-1280.png` — Ayu (Light / Dark) · light · 1280px
- `theme-catppuccin-frappe-overview-dark-1280.png` — Catppuccin (Latte / Frappé) · dark · 1280px
- `theme-catppuccin-frappe-overview-light-1280.png` — Catppuccin (Latte / Frappé) · light · 1280px
- `theme-catppuccin-macchiato-overview-dark-1280.png` — Catppuccin (Latte / Macchiato) · dark · 1280px
- `theme-catppuccin-macchiato-overview-light-1280.png` — Catppuccin (Latte / Macchiato) · light · 1280px
- `theme-catppuccin-overview-dark-1280.png` — Catppuccin (Latte / Mocha) · dark · 1280px
- `theme-catppuccin-overview-light-1280.png` — Catppuccin (Latte / Mocha) · light · 1280px
- `theme-default-overview-dark-1280.png` — shadcn default (neutral) · dark · 1280px
- `theme-default-overview-light-1280.png` — shadcn default (neutral) · light · 1280px
- `theme-dracula-overview-dark-1280.png` — Dracula (Alucard / Dracula) · dark · 1280px
- `theme-dracula-overview-light-1280.png` — Dracula (Alucard / Dracula) · light · 1280px
- `theme-everforest-overview-dark-1280.png` — Everforest (Light / Dark) · dark · 1280px
- `theme-everforest-overview-light-1280.png` — Everforest (Light / Dark) · light · 1280px
- `theme-github-overview-dark-1280.png` — GitHub (Light / Dark) · dark · 1280px
- `theme-github-overview-light-1280.png` — GitHub (Light / Dark) · light · 1280px
- `theme-gruvbox-overview-dark-1280.png` — Gruvbox (Light / Dark) · dark · 1280px
- `theme-gruvbox-overview-light-1280.png` — Gruvbox (Light / Dark) · light · 1280px
- `theme-kanagawa-dragon-overview-dark-1280.png` — Kanagawa (Lotus / Dragon) · dark · 1280px
- `theme-kanagawa-dragon-overview-light-1280.png` — Kanagawa (Lotus / Dragon) · light · 1280px
- `theme-kanagawa-overview-dark-1280.png` — Kanagawa (Lotus / Wave) · dark · 1280px
- `theme-kanagawa-overview-light-1280.png` — Kanagawa (Lotus / Wave) · light · 1280px
- `theme-monokai-overview-dark-1280.png` — Monokai (derived light / Monokai) · dark · 1280px
- `theme-monokai-overview-light-1280.png` — Monokai (derived light / Monokai) · light · 1280px
- `theme-nord-overview-dark-1280.png` — Nord (Snow Storm / Polar Night) · dark · 1280px
- `theme-nord-overview-light-1280.png` — Nord (Snow Storm / Polar Night) · light · 1280px
- `theme-one-dark-overview-dark-1280.png` — One Light / One Dark · dark · 1280px
- `theme-one-dark-overview-light-1280.png` — One Light / One Dark · light · 1280px
- `theme-rose-pine-moon-overview-dark-1280.png` — Rosé Pine (Dawn / Moon) · dark · 1280px
- `theme-rose-pine-moon-overview-light-1280.png` — Rosé Pine (Dawn / Moon) · light · 1280px
- `theme-rose-pine-overview-dark-1280.png` — Rosé Pine (Dawn / Main) · dark · 1280px
- `theme-rose-pine-overview-light-1280.png` — Rosé Pine (Dawn / Main) · light · 1280px
- `theme-solarized-overview-dark-1280.png` — Solarized (Light / Dark) · dark · 1280px
- `theme-solarized-overview-light-1280.png` — Solarized (Light / Dark) · light · 1280px
- `theme-tokyo-night-moon-overview-dark-1280.png` — Tokyo Night (Day / Moon) · dark · 1280px
- `theme-tokyo-night-moon-overview-light-1280.png` — Tokyo Night (Day / Moon) · light · 1280px
- `theme-tokyo-night-overview-dark-1280.png` — Tokyo Night (Day / Night) · dark · 1280px
- `theme-tokyo-night-overview-light-1280.png` — Tokyo Night (Day / Night) · light · 1280px
- `theme-tokyo-night-storm-overview-dark-1280.png` — Tokyo Night (Day / Storm) · dark · 1280px
- `theme-tokyo-night-storm-overview-light-1280.png` — Tokyo Night (Day / Storm) · light · 1280px

## Views — shadcn default, light + dark (1280, full page)

- `view-default-overview-light-1280.png` — Dashboard · shadcn default (neutral) · light
- `view-default-overview-dark-1280.png` — Dashboard · shadcn default (neutral) · dark
- `view-default-analytics-light-1280.png` — Analytics · shadcn default (neutral) · light
- `view-default-analytics-dark-1280.png` — Analytics · shadcn default (neutral) · dark
- `view-default-customers-light-1280.png` — Customers (data table) · shadcn default (neutral) · light
- `view-default-customers-dark-1280.png` — Customers (data table) · shadcn default (neutral) · dark
- `view-default-customer-light-1280.png` — Customer detail · shadcn default (neutral) · light
- `view-default-customer-dark-1280.png` — Customer detail · shadcn default (neutral) · dark
- `view-default-settings-general-light-1280.png` — Settings · General · shadcn default (neutral) · light
- `view-default-settings-general-dark-1280.png` — Settings · General · shadcn default (neutral) · dark
- `view-default-settings-members-light-1280.png` — Settings · Members · shadcn default (neutral) · light
- `view-default-settings-members-dark-1280.png` — Settings · Members · shadcn default (neutral) · dark
- `view-default-settings-billing-light-1280.png` — Settings · Billing · shadcn default (neutral) · light
- `view-default-settings-billing-dark-1280.png` — Settings · Billing · shadcn default (neutral) · dark
- `view-default-settings-notifications-light-1280.png` — Settings · Notifications · shadcn default (neutral) · light
- `view-default-settings-notifications-dark-1280.png` — Settings · Notifications · shadcn default (neutral) · dark
- `view-default-settings-api-light-1280.png` — Settings · API keys · shadcn default (neutral) · light
- `view-default-settings-api-dark-1280.png` — Settings · API keys · shadcn default (neutral) · dark
- `view-default-login-light-1280.png` — Login · shadcn default (neutral) · light
- `view-default-login-dark-1280.png` — Login · shadcn default (neutral) · dark
- `view-default-signup-light-1280.png` — Sign up · shadcn default (neutral) · light
- `view-default-signup-dark-1280.png` — Sign up · shadcn default (neutral) · dark
- `view-default-otp-light-1280.png` — Verify code (OTP) · shadcn default (neutral) · light
- `view-default-otp-dark-1280.png` — Verify code (OTP) · shadcn default (neutral) · dark
- `view-default-onboarding-light-1280.png` — Onboarding · shadcn default (neutral) · light
- `view-default-onboarding-dark-1280.png` — Onboarding · shadcn default (neutral) · dark
- `view-default-not-found-light-1280.png` — 404 · shadcn default (neutral) · light
- `view-default-not-found-dark-1280.png` — 404 · shadcn default (neutral) · dark

## Views — Tokyo Night dark (1280, full page)

- `view-tokyo-night-overview-dark-1280.png` — Dashboard · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-analytics-dark-1280.png` — Analytics · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-customers-dark-1280.png` — Customers (data table) · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-customer-dark-1280.png` — Customer detail · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-settings-general-dark-1280.png` — Settings · General · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-settings-members-dark-1280.png` — Settings · Members · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-settings-billing-dark-1280.png` — Settings · Billing · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-settings-notifications-dark-1280.png` — Settings · Notifications · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-settings-api-dark-1280.png` — Settings · API keys · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-login-dark-1280.png` — Login · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-signup-dark-1280.png` — Sign up · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-otp-dark-1280.png` — Verify code (OTP) · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-onboarding-dark-1280.png` — Onboarding · Tokyo Night (Day / Night) · dark
- `view-tokyo-night-not-found-dark-1280.png` — 404 · Tokyo Night (Day / Night) · dark

## Views — Catppuccin Latte (1280, full page)

- `view-catppuccin-overview-light-1280.png` — Dashboard · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-analytics-light-1280.png` — Analytics · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-customers-light-1280.png` — Customers (data table) · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-customer-light-1280.png` — Customer detail · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-settings-general-light-1280.png` — Settings · General · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-settings-members-light-1280.png` — Settings · Members · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-settings-billing-light-1280.png` — Settings · Billing · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-settings-notifications-light-1280.png` — Settings · Notifications · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-settings-api-light-1280.png` — Settings · API keys · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-login-light-1280.png` — Login · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-signup-light-1280.png` — Sign up · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-otp-light-1280.png` — Verify code (OTP) · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-onboarding-light-1280.png` — Onboarding · Catppuccin (Latte / Mocha) · light
- `view-catppuccin-not-found-light-1280.png` — 404 · Catppuccin (Latte / Mocha) · light

## States — loading / empty / error (default, light)

- `state-default-analytics-loading-light-1280.png` — Analytics — loading state · shadcn default (neutral) · light
- `state-default-customers-empty-light-1280.png` — Customers (data table) — empty state · shadcn default (neutral) · light
- `state-default-customers-error-light-1280.png` — Customers (data table) — error state · shadcn default (neutral) · light
- `state-default-overview-loading-light-1280.png` — Dashboard — loading state · shadcn default (neutral) · light

## Overlays — command palette, dialog, confirm, sheet, toast, mobile nav

- `overlay-default-customers-confirm-light-1280.png` — Customers (data table) + overlay confirm · shadcn default (neutral) · light · 1280px
- `overlay-default-customers-sheet-light-1280.png` — Customers (data table) + overlay sheet · shadcn default (neutral) · light · 1280px
- `overlay-default-customers-toast-light-1280.png` — Customers (data table) + overlay toast · shadcn default (neutral) · light · 1280px
- `overlay-default-overview-command-dark-1280.png` — Dashboard + overlay command · shadcn default (neutral) · dark · 1280px
- `overlay-default-overview-command-light-1280.png` — Dashboard + overlay command · shadcn default (neutral) · light · 1280px
- `overlay-default-overview-mobile-nav-light-390.png` — Dashboard + overlay mobile-nav · shadcn default (neutral) · light · 390px
- `overlay-default-settings-members-dialog-dark-1280.png` — Settings · Members + overlay dialog · shadcn default (neutral) · dark · 1280px
- `overlay-default-settings-members-dialog-light-1280.png` — Settings · Members + overlay dialog · shadcn default (neutral) · light · 1280px

## Shell B — top navigation (default, light, full page)

- `shellB-default-overview-light-1280.png` — Dashboard · top-nav shell · shadcn default (neutral) · light
- `shellB-default-customers-light-1280.png` — Customers (data table) · top-nav shell · shadcn default (neutral) · light
- `shellB-default-settings-general-light-1280.png` — Settings · General · top-nav shell · shadcn default (neutral) · light

## Styles — the 8 shadcn styles on the Dashboard (default, light)

- `style-luma-default-overview-light-1280.png` — style luma · shadcn default (neutral) · light
- `style-lyra-default-overview-light-1280.png` — style lyra · shadcn default (neutral) · light
- `style-maia-default-overview-light-1280.png` — style maia · shadcn default (neutral) · light
- `style-mira-default-overview-light-1280.png` — style mira · shadcn default (neutral) · light
- `style-nova-default-overview-light-1280.png` — style nova · shadcn default (neutral) · light
- `style-rhea-default-overview-light-1280.png` — style rhea · shadcn default (neutral) · light
- `style-sera-default-overview-light-1280.png` — style sera · shadcn default (neutral) · light
- `style-vega-default-overview-light-1280.png` — style vega · shadcn default (neutral) · light

## Mobile — 390px (full page)

- `mobile-default-overview-light-390.png` — Dashboard · shadcn default (neutral) · light · 390px
- `mobile-default-overview-dark-390.png` — Dashboard · shadcn default (neutral) · dark · 390px
- `mobile-default-analytics-light-390.png` — Analytics · shadcn default (neutral) · light · 390px
- `mobile-default-customers-light-390.png` — Customers (data table) · shadcn default (neutral) · light · 390px
- `mobile-default-customers-dark-390.png` — Customers (data table) · shadcn default (neutral) · dark · 390px
- `mobile-default-customer-light-390.png` — Customer detail · shadcn default (neutral) · light · 390px
- `mobile-default-settings-general-light-390.png` — Settings · General · shadcn default (neutral) · light · 390px
- `mobile-default-login-light-390.png` — Login · shadcn default (neutral) · light · 390px
- `mobile-default-signup-light-390.png` — Sign up · shadcn default (neutral) · light · 390px
- `mobile-default-onboarding-light-390.png` — Onboarding · shadcn default (neutral) · light · 390px

## What the simulation approximates

A zero-dependency CSS approximation of shadcn's Nova/Vega component styles on top of the real token contract: colors and tiers are exact,
geometry is close (±2px). Real builds use the installed shadcn components; expect identical colors and slightly different control details.
Charts are SVG stand-ins for Recharts (stacked gradient area, grouped bars, lines with dots, donut with center label, radial progress).
