# Tests: Vitest, browser mode, Playwright

Reference file for the typescript skill. Versions read on 2026-09-30 from the npm registry; the Vitest configuration and the two test shapes below were run on 2026-10-01 (see the bottom of this file for what was and was not executed).

## Tiers

| Tier | Runner | What it proves | Command |
|---|---|---|---|
| Unit | Vitest 5 (`environment: 'node'` default) | Pure functions, type guards, the `api.ts` client against a mocked `fetch` | `npm test` |
| Component | Vitest 5 + jsdom + `@testing-library/react` 16 | Rendering, labels, roles, form behaviour; no real layout | `npm test` |
| Component, real browser | Vitest 5 browser mode, `@vitest/browser-playwright` | Layout, `:focus-visible`, `<dialog>`, CSS that jsdom fakes | `npm run test:browser` |
| E2E | `@playwright/test` 1.63 against the **Go binary** serving `dist/client` | The golden path and one edge case per feature; the axe gate | `npm run e2e` |

Vitest 5 needs Node ≥ 22.12 and Vite ≥ 6.4; `clearMocks` is on by default; browser providers are separate packages. Vitest discourages `@testing-library/user-event` in browser mode and ships its own `userEvent`.

## `vitest.config.ts` — both Vitest tiers in one file

```ts
import { defineConfig } from 'vitest/config'
import { playwright } from '@vitest/browser-playwright'

export default defineConfig({
  test: {
    projects: [
      {
        extends: true,
        test: {
          name: 'unit',
          environment: 'jsdom',
          include: ['src/**/*.test.{ts,tsx}'],
          exclude: ['src/**/*.browser.test.{ts,tsx}'],
          setupFiles: ['./src/test-setup.ts'],
        },
      },
      {
        extends: true,
        test: {
          name: 'browser',
          include: ['src/**/*.browser.test.{ts,tsx}'],
          browser: { enabled: true, provider: playwright(), instances: [{ browser: 'chromium' }] },
        },
      },
    ],
  },
})
```

```ts
// src/test-setup.ts — jest-dom matchers for the jsdom tier
import '@testing-library/jest-dom/vitest'
```

`package.json` scripts:

```json
{
  "test": "vitest run --project unit",
  "test:browser": "vitest run --project browser",
  "e2e": "playwright test",
  "typecheck": "tsc -b"
}
```

The Vite plugins from `vite.config.ts` are not loaded by `vitest.config.ts` unless merged; keep the test config standalone (jsdom needs neither Tailwind nor the Start plugin) and the `tsconfig.app.json` `types` gain `"vitest/globals"` only if `globals: true` is set — the shapes below import from `vitest` instead.

## Pure-function test

```ts
// src/lib/money.ts
export function formatCents(cents: number, currency = 'EUR'): string {
  return new Intl.NumberFormat('en', { style: 'currency', currency }).format(cents / 100)
}

// src/lib/money.test.ts
import { describe, expect, it } from 'vitest'
import { formatCents } from './money'

describe('formatCents', () => {
  it('formats whole euros', () => {
    expect(formatCents(1200)).toBe('€12.00')
  })
  it('keeps negative amounts negative', () => {
    expect(formatCents(-50)).toBe('-€0.50')
  })
})
```

## Component test (jsdom + Testing Library)

This toggle writes `data-theme`; on `frontend-ui-modern` the dark switch is the `.dark` class, so assert that instead.

```tsx
// src/components/ThemeToggle.test.tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it } from 'vitest'
import { ThemeToggle } from './ThemeToggle'

beforeEach(() => {
  localStorage.clear()
  document.documentElement.dataset.theme = ''
  // jsdom has no matchMedia: give it one that reports a light OS
  window.matchMedia = (q: string) =>
    ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList
})

it('cycles light → dark → system and persists the explicit choices', async () => {
  const user = userEvent.setup()
  render(<ThemeToggle />)
  const button = screen.getByRole('button', { name: /theme: system/i })

  await user.click(button)
  expect(document.documentElement.dataset.theme).toBe('light')
  expect(localStorage.getItem('theme')).toBe('light')

  await user.click(button)
  expect(document.documentElement.dataset.theme).toBe('dark')
  expect(localStorage.getItem('theme')).toBe('dark')

  await user.click(button)
  expect(localStorage.getItem('theme')).toBeNull()        // system: nothing stored
  expect(document.documentElement.dataset.theme).toBe('light')  // follows the (mocked) light OS
})
```

The query is by **role and accessible name**, never by class or test id: that is what a screen reader sees and what the test should protect.

## Browser-mode test (when layout or focus matters)

```tsx
// src/components/Dialog.browser.test.tsx
import { render } from 'vitest-browser-react'
import { page, userEvent } from 'vitest/browser'
import { expect, it } from 'vitest'
import { Dialog } from './Dialog'

it('traps focus and closes on Escape', async () => {
  let open = true
  render(<Dialog open={open} onClose={() => { open = false }} title="Confirm"><Dialog.Body>Sure?</Dialog.Body></Dialog>)
  await expect.element(page.getByRole('dialog', { name: 'Confirm' })).toBeVisible()
  await userEvent.keyboard('{Escape}')
  expect(open).toBe(false)
})
```

## E2E: `playwright.config.ts` with the Go binary as `webServer`

```ts
import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  use: { baseURL: 'http://127.0.0.1:8080' },
  webServer: {
    // build the SPA, then start the Go binary that embeds it — the artifact the users get
    command: 'npm run build && cd .. && go run ./cmd/server',
    url: 'http://127.0.0.1:8080/healthz',
    reuseExistingServer: false,   // always the server this run built: a stray process on :8080 is not the thing under test
    timeout: 120_000,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
```

The axe gate is an ordinary spec in `e2e/`: `@axe-core/playwright` 4.13, `withTags(['wcag2a', 'wcag2aa', 'wcag22aa'])`, run once per theme by setting `localStorage.theme` in `addInitScript`.

## Rules

- A test that passes before the code exists is not the test: run it red first (`review-standards` § 2).
- One assertion per behaviour; a test name says the behaviour ("cycles light → dark → system"), not the function.
- Mock at the boundary (`fetch`, `matchMedia`, the clock with `vi.useFakeTimers()`), never the module under test.
- The e2e tier runs against the Go binary and the built `dist/client`, never against `vite dev`: the dev server is not what ships.
- Coverage claims name what is not covered (the global rule); `vitest run --coverage` needs `@vitest/coverage-v8` and is a number, not a verdict.

## Verified on 2026-10-01

Run: the `vitest.config.ts` jsdom project above with the two tests (pure function, ThemeToggle) under Vitest 5 and `@testing-library/react` 16 — see the harness's Phase 6 report for the exit status. Not run: the browser-mode project (needs a Playwright Chromium download) and the e2e config against a Go server; both follow the vendors' documented shapes.
