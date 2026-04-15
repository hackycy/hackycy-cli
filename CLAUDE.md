# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`hackycy-cli` (invoked as `ycy`) is a personal developer CLI toolkit with four commands:
- `ycy did <directory>` — scan a directory tree for Git repos and generate a commit history report
- `ycy serve <directory>` — static file HTTP server with directory listing UI (default port 1204)
- `ycy zip <directory>` — interactively zip a directory with glob-pattern filtering
- `ycy rp` - run package.json scripts
- `ycy upgrade` — self-update by fetching the latest release from GitHub

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

**Entry point**: `src/cli.ts` — registers all commands using `commander`. Each command module is lazy-loaded via dynamic `import()` to keep startup fast.

**One file per command**: `src/did.ts`, `src/serve.ts`, `src/zip.ts`, `src/upgrade.ts`. Each exports a typed options interface and a main async function called by `cli.ts`.

**Shared utilities**: `src/utils.ts` — `clearScreen()`, `printTitle()`, `hyperlinker()` (OSC 8 terminal links).

**Key dependencies**:
- `@clack/prompts` — interactive terminal UI (spinners, selects, text inputs)
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

## Frontend

Use HTML imports with `Bun.serve()`. Don't use `vite`. HTML imports fully support React, CSS, Tailwind.

Server:

```ts#index.ts
import index from "./index.html"

Bun.serve({
  routes: {
    "/": index,
    "/api/users/:id": {
      GET: (req) => {
        return new Response(JSON.stringify({ id: req.params.id }));
      },
    },
  },
  // optional websocket support
  websocket: {
    open: (ws) => {
      ws.send("Hello, world!");
    },
    message: (ws, message) => {
      ws.send(message);
    },
    close: (ws) => {
      // handle close
    }
  },
  development: {
    hmr: true,
    console: true,
  }
})
```

HTML files can import .tsx, .jsx or .js files directly and Bun's bundler will transpile & bundle automatically. `<link>` tags can point to stylesheets and Bun's CSS bundler will bundle.

```html#index.html
<html>
  <body>
    <h1>Hello, world!</h1>
    <script type="module" src="./frontend.tsx"></script>
  </body>
</html>
```

With the following `frontend.tsx`:

```tsx#frontend.tsx
import React from "react";
import { createRoot } from "react-dom/client";

// import .css files directly and it works
import './index.css';

const root = createRoot(document.body);

export default function Frontend() {
  return <h1>Hello, world!</h1>;
}

root.render(<Frontend />);
```

Then, run index.ts

```sh
bun --hot ./index.ts
```

For more information, read the Bun API docs in `node_modules/bun-types/docs/**.mdx`.
