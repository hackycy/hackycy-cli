# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`hackycy-cli` (invoked as `ycy`) is a personal developer CLI toolkit

## Commands

```sh
bun run dev        # watch-mode dev run (src/index.ts)
bun run lint       # ESLint with cache
bun run typecheck  # tsc type-check only (no emit)
bun run release    # bumpp version bump + tag
bun src/cli.ts     # run CLI directly (manual testing)
```

No automated tests exist; manual testing is done with `bun src/cli.ts`.

## Architecture

**Entry point**: `src/cli.ts` — thin orchestrator that calls `register(program)` for each command module and sets up `errorHandler`. No command logic lives here.

**Directory layout**:
```
src/
├── cli.ts                    # program setup, errorHandler only
├── shared/
│   └── utils.ts              # clearScreen, printTitle, hyperlinker, parseIntArg
└── commands/
    └── <name>/
        ├── index.ts          # exports register(program: Command): void
        ├── types.ts          # exported option interfaces (omit if no shared types)
        └── <name>.ts         # implementation (lazy-imported inside action handlers)
```

**Command registration**: every command (and future sub-command group) must export `register(program: Command): void` from its `index.ts`. `cli.ts` imports and calls these; it never contains inline command logic.

**Multi-level commands**: commands are grouped by functional domain (e.g. `ycy foo bar`). A sub-command group gets its own directory under `commands/`, with the same `index.ts` / `types.ts` / implementation layout. Sub-group `register` receives the parent `Command` object and calls `parent.addCommand(...)` directly.

**Lazy loading**: heavy implementation files are always dynamically imported (`await import('./did')`) inside action handlers, never at module top-level, to keep startup time fast.

**Shared utilities**: `src/shared/utils.ts` — `clearScreen()`, `printTitle()`, `hyperlinker()` (OSC 8 terminal links), `parseIntArg()`. This is the only global utility module; group-specific helpers live alongside the group's own files.

**Types**: each command group keeps its exported option interfaces in its own `types.ts`. Internal-only types stay in the implementation file. Do not create a global `src/types.ts`.

**Key dependencies**:
- `@clack/prompts` — interactive terminal UI (spinners, selects, text inputs)
- `ink` - react for interactive command-line apps
- `commander` — CLI argument parsing
- `ansis` — terminal colors
- `dayjs` — date math in `did`
- `fflate` — zip compression in `zip`
- `reveal-file` — open Finder/Explorer after zip

**Runtime APIs used**: `Bun.spawn` (git), `Bun.serve()` (static server), `Bun.file`/`Bun.write`, `Bun.Glob`, `Bun.semver`

**TypeScript config**: `noEmit: true` — Bun transpiles at runtime, `tsc` is type-check only. Module resolution is `Preserve` (bundler mode).

## Release / Distribution

Builds are distributed as pre-compiled native binaries for macOS (x64/arm64), Linux (x64/arm64), and Windows (x64) via GitHub Releases. The `upgrade` command handles self-replacement by downloading the matching binary artifact.

---

Default to using Bun instead of Node.js.

- Use `bun <file>` instead of `node <file>` or `ts-node <file>`
- Use `bun test` instead of `jest` or `vitest`
- Use `bun build <file.html|file.ts|file.css>` instead of `webpack` or `esbuild`
- Use `bun install` instead of `npm install` or `yarn install` or `pnpm install`
- Use `bun run <script>` instead of `npm run <script>` or `yarn run <script>` or `pnpm run <script>`
- Use `bunx <package> <command>` instead of `npx <package> <command>`
- Bun automatically loads .env, so don't use dotenv.

## APIs

- `Bun.serve()` supports WebSockets, HTTPS, and routes. Don't use `express`.
- `bun:sqlite` for SQLite. Don't use `better-sqlite3`.
- `Bun.redis` for Redis. Don't use `ioredis`.
- `Bun.sql` for Postgres. Don't use `pg` or `postgres.js`.
- `WebSocket` is built-in. Don't use `ws`.
- Prefer `Bun.file` over `node:fs`'s readFile/writeFile
- Bun.$`ls` instead of execa.

## Testing

Use `bun test` to run tests.

```ts#index.test.ts
import { test, expect } from "bun:test";

test("hello world", () => {
  expect(1).toBe(1);
});
```
