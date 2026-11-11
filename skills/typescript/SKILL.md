---
name: typescript
description: |
  TypeScript language standards: typing discipline, tsconfig, generics, narrowing,
  module/ESM conventions, runtime validation at boundaries, and the test tiers
  (Vitest, browser mode, Playwright). Use when writing or reviewing TypeScript code,
  fixing type errors, designing types/interfaces, configuring tsconfig, or setting up
  tests — in any TS codebase, frontend or tooling.
  Do NOT use for UI, styling or components (use frontend-ui-modern),
  backend API logic (use golang), or shell automation (use bash).
---

# TypeScript

Language-level standards for all TypeScript code. UI conventions live in the `frontend-ui-modern` and `frontend-ui-economist` skills.

- **Frontend: TanStack Start + TypeScript + Tailwind** in one `web/` or `ui/` directory (`typescript` skill; `frontend-ui-modern` for shadcn/ui app UI; `frontend-ui-economist` for plain HTML/CSS editorial/report UI). Client JS runs on plain V8: no Node APIs outside server entry.

## Compiler Baseline

- TypeScript 7.0 (the native compiler; `latest` since 2026-07, 7.0.2 still `latest` on the npm registry on 2026-10-25) with 6.0's defaults: `strict: true`, `module: esnext`, `types: []`. Removed options (`moduleResolution: node`/`classic`, `baseUrl`, `esModuleInterop: false`, `target: es5`) fail the build; do not carry them over.
- The app config: `moduleResolution: bundler`, `verbatimModuleSyntax`, `moduleDetection: force`, `erasableSyntaxOnly`, `noUncheckedIndexedAccess`, `noImplicitOverride`, `types: ["vite/client"]`, `noEmit`. Vite transpiles without type checking, so `tsc -b` is the type gate: `npm run build` = `vite build && tsc -b` (Vite first; it generates the route tree `tsc` needs). The copy-paste `tsconfig.json` + `tsconfig.app.json` + `tsconfig.node.json` and a line per flag: `./references/tsconfig.md`.
- **Plain V8:** shipped JS runs in the browser with no Node. Nothing under `src/` imports `node:*`, `fs`, `path`, `process`, `Buffer` or `__dirname`; `types: ["vite/client"]` in the app project makes that a compile error, and `@types/node` lives only in `tsconfig.node.json` for `vite.config.ts`, the one Node file. The reasoning is in `./references/tsconfig.md` § The plain-V8 rule.
- One config per package; extend a shared base instead of copying options.

## Typing Discipline

- **No `any`.** Use `unknown` at untrusted boundaries and narrow before use.
- Type every exported function signature, prop, and API payload explicitly.
- Prefer type inference for locals — annotate boundaries, not every variable.
- Model variants as **discriminated unions**, not optional-field grab bags or enums.
- Use `satisfies` for config objects to keep literal types while enforcing shape.
- Prefer `readonly` arrays/properties for data that shouldn't mutate.
- Derive types from values where possible (`typeof`, `ReturnType`, `as const`) instead of maintaining parallel type declarations.

## Boundaries & Validation

- Compile-time types vanish at runtime: validate external input (HTTP responses, env vars, form data, JSON) at the boundary, then trust types inward.
- A hand-written type guard (`function isUser(x: unknown): x is User`) is the default. Zod 4 (`z.email()`, `z.url()`, the top-level formats; `z.string().email()` is deprecated) only where a schema is reused, by a form library or on more than one boundary.
- Keep API payload types in one shared location (`lib/`) — never redefine them per call site.

## Modules & Style

- ESM only: `import`/`export`, no `require`. Use `import type` for type-only imports (`verbatimModuleSyntax` enforces it).
- `erasableSyntaxOnly` means no `enum`, no runtime `namespace`, no parameter properties, no `import =`/`export =`: `as const` objects and unions instead.
- No `/// <reference>`, no default exports for shared modules — named exports keep renames refactor-safe.
- Errors: throw `Error` subclasses, never strings; type caught errors as `unknown` and narrow.

## Tests

Four tiers, one runner per tier: Vitest 5 with jsdom + Testing Library 16 for units and component logic (`npm test`); Vitest browser mode with `@vitest/browser-playwright` when layout or `:focus-visible` matters (`npm run test:browser`); `@playwright/test` 1.63 for e2e against the **Go binary** serving `dist/client`, never against `vite dev` (`npm run e2e`). Configs, one pure-function test, one component test and the Playwright `webServer` block: `./references/testing.md`. A test is run red before the code exists (`review-standards` § 2).

## Before claiming done

Apply `review-standards` § 8 (the code-shape rules): a guard keeps the data when its check errors, a read-only mode performs no side effect, a generated artifact (route tree, embedded build) is pinned both ways to its source, a verification command is never chained to a commit.

## Escape Hatches

- `as` casts and `!` non-null assertions are last resorts — each one needs a comment explaining why the compiler is wrong.
- `@ts-expect-error` (with reason) over `@ts-ignore`; fix the type instead when feasible.

## Reference Library

* **tsconfig (the three-file shape for a Vite + TanStack Start app, a line per flag, the plain-V8 rule)**
  Read → `./references/tsconfig.md`
* **Tests (Vitest 5 config with the jsdom and browser projects, test shapes, Playwright with the Go binary as webServer)**
  Read → `./references/testing.md`
