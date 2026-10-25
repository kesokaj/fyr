# tsconfig for a TanStack Start app that ships to plain V8

Reference file for the typescript skill. Copy-paste shapes, verified on 2026-10-01 with TypeScript 7.0.2 (`tsc -b` exit 0 on a TanStack Start 1.168 + Vite 8.3 app under Node 22.21), and again on 2026-10-25 (TypeScript 7.0.2, TanStack Start 1.169, Vite 8.3.5, `node:24-alpine` with Node 24.22.1).

## The shape: one root, two projects

Vite transpiles with Oxc and does **not** type-check, and it ignores tsconfig `target`. So the app config is `noEmit` with `tsc -b` as the type gate, and the Vite config file gets its own project with Node types — the only place `@types/node` is allowed. That split is what makes "no Node-only APIs outside the server entry" a compile error instead of a code-review rule: `src/` has `types: ["vite/client"]`, so `import fs from 'node:fs'`, `process.env`, `Buffer` and `__dirname` fail to type-check there.

```jsonc
// web/tsconfig.json — the root: no files, two references
{
  "files": [],
  "references": [{ "path": "./tsconfig.app.json" }, { "path": "./tsconfig.node.json" }]
}
```

```jsonc
// web/tsconfig.app.json — everything under src/
{
  "compilerOptions": {
    "target": "es2023",
    "lib": ["ES2023", "DOM", "DOM.Iterable"],
    "module": "esnext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",
    "types": ["vite/client"],

    "verbatimModuleSyntax": true,
    "moduleDetection": "force",
    "erasableSyntaxOnly": true,
    "allowImportingTsExtensions": true,
    "noEmit": true,
    "skipLibCheck": true,

    "strict": true,
    "noUncheckedIndexedAccess": true,
    "noImplicitOverride": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true
  },
  "include": ["src"]
}
```

```jsonc
// web/tsconfig.node.json — vite.config.ts only; the one project with Node types
{
  "compilerOptions": {
    "target": "es2023",
    "lib": ["ES2023"],
    "module": "esnext",
    "moduleResolution": "bundler",
    "types": ["node"],
    "verbatimModuleSyntax": true,
    "moduleDetection": "force",
    "noEmit": true,
    "skipLibCheck": true,
    "strict": true
  },
  "include": ["vite.config.ts"]
}
```

Scripts: `"build": "vite build && tsc -b"` — Vite first, because the Start plugin generates `src/routeTree.gen.ts` during the build and a fresh clone has no such file for `tsc` to find (TS2307, measured). `"typecheck": "tsc -b"` for the editor loop once the tree exists.

## Why each flag

| Flag | Why | Reference |
|---|---|---|
| `module: esnext`, `moduleResolution: bundler` | Vite resolves like a bundler: `exports` maps, extensionless imports, no `require`. TS 6 removed `node`/`classic` resolution and `baseUrl`. | [tsconfig reference](https://www.typescriptlang.org/tsconfig/) |
| `verbatimModuleSyntax` | Imports are kept exactly as written and type-only imports must say `import type`, which a per-file transpiler needs; supersedes `isolatedModules`, `preserveValueImports`, `importsNotUsedAsValues`. | [verbatimModuleSyntax](https://www.typescriptlang.org/tsconfig/verbatimModuleSyntax.html) |
| `moduleDetection: force` | Every `.ts`/`.tsx` is a module even without an import or export; no accidental script-scope globals. | [moduleDetection](https://www.typescriptlang.org/tsconfig/moduleDetection.html) |
| `erasableSyntaxOnly` | Only syntax a transpiler can strip: **no `enum`, no runtime `namespace`, no parameter properties, no `import =`/`export =`, no `<T>x` assertions.** Unions and `as const` objects instead of enums (the skill's rule, now enforced). | [erasableSyntaxOnly](https://www.typescriptlang.org/tsconfig/erasableSyntaxOnly.html) |
| `types: ["vite/client"]` (app) / `["node"]` (node) | `import.meta.env`, `?url` imports and CSS modules typed in `src/`; Node globals only where Node runs. `@types/node` is a devDependency used by one file. | [Vite client types](https://vite.dev/guide/features.html#client-types) |
| `noEmit` + `allowImportingTsExtensions` | `tsc` checks, Vite emits; `.ts` extensions in imports are legal only with `noEmit`. | tsconfig reference |
| `target: es2023`, `lib` | The browser floor Tailwind 4 and Vite 8 share (Safari 16.4, Chrome 111, Firefox 128) supports ES2023; Vite ignores this field for output, `tsc` uses it for `lib` checks. | [Vite features](https://vite.dev/guide/features.html) |
| `strict`, `noUncheckedIndexedAccess`, `noImplicitOverride` | `arr[i]` is `T \| undefined`; an override must say `override`. TS 6 made `strict` the default; the other two stay explicit. | tsconfig reference |
| `noUnusedLocals`, `noUnusedParameters`, `noFallthroughCasesInSwitch` | Dead code and fall-through are errors, not lint warnings. | tsconfig reference |
| `skipLibCheck` | `node_modules` `.d.ts` files are not re-checked; the project's own types are. | tsconfig reference |

TypeScript 6.0 defaults (2026-03) carried into 7.0: `strict: true`, `module: esnext`, `types: []`, `noUncheckedSideEffectImports: true`; removed: `moduleResolution: node`/`classic`, `baseUrl`, `esModuleInterop: false`, `target: es5`. A tsconfig that still sets any of the removed ones fails on 7.0.

## The plain-V8 rule, once

Shipped JS runs on plain V8 in the browser: nothing under `src/` imports `node:*`, `fs`, `path`, `process`, `Buffer` or `__dirname`; only `VITE_*` keys of `import.meta.env` reach the bundle. The reasoning: the Go binary serves `dist/client` and there is no Node at runtime, so a Node API in the bundle is a runtime `ReferenceError` the type checker would otherwise not see. `types: ["vite/client"]` in the app project is the enforcement; the Vite config file (the server entry of the build, the only Node code in `web/`) sits in `tsconfig.node.json` with `types: ["node"]`.

## Checking a tsconfig

```bash
npx tsc -b                       # the project references, both projects, exit 0 or the errors
npx tsc --showConfig -p tsconfig.app.json   # the effective options after defaults
```
