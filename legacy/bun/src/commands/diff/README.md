# Directory Diff Command

This directory contains the complete implementation of `ycy diff`: a read-only directory comparison engine, a same-origin HTTP server, a stateless MCP interface, and an embedded React application.

Treat this README as the living development document for the module. Update it when comparison semantics, API contracts, security boundaries, performance limits, or ownership between modules changes.

## Purpose

```text
ycy diff [options] <baseline-directory> <target-directory>

Options:
  -p, --port <number>       Port to serve on (default: 1205)
      --public              Make the diff available on the local network
  -x, --exclude <glob>      Add an exclusion; may be repeated
      --no-gitignore        Do not apply Target Directory .gitignore files
```

The comparison is directional:

- Baseline is the old side.
- Target is the new side.
- Added exists only in Target.
- Deleted exists only in Baseline.
- Modified exists on both sides but differs in kind, symlink target, size, or bytes.
- Unchanged exists on both sides with the same kind and identical bytes or symlink target.

The command resolves both roots to real absolute paths, records their directory identities, starts the HTTP server, prints the URL, and builds the first snapshot. Equal roots are rejected. Nested roots are valid because traversal never follows symlinks. A Refresh fails closed if either root has been replaced since service start.

## Local Development

Run the command from source:

```bash
bun src/cli.ts diff ./baseline ./target
```

Useful checks:

```bash
bun test src/commands/diff
bun run typecheck
bun run lint --no-cache
bun scripts/build.ts --outfile .tmp/ycy
```

`bunfig.toml` registers `bun-plugin-tailwind` for source-mode HTML bundling. `scripts/build.ts` uses the same plugin when compiling the standalone executable, so changes to the web entrypoint must work in both modes.

## Source Map

| Path | Responsibility |
| --- | --- |
| `index.ts` | Commander registration and CLI option parsing. |
| `run.ts` | Root resolution, workspace/server composition, process signals, and LAN URL presentation. |
| `types.ts` | Domain types and the internal workspace/snapshot interfaces. |
| `workspace.ts` | Traversal, ignore rules, byte comparison, snapshots, queries, content classification, and stale checks. |
| `server.ts` | Same-origin HTTP adapter, request validation, security headers, SSE, and server lifecycle. |
| `mcp.ts` | Stateless Streamable HTTP MCP adapter, tool schemas, and structured result mapping. |
| `web/api.ts` | Browser-side API shapes and URL helpers. |
| `web/app.tsx` | Application state, toolbar, status filters, responsive layout, tabs, and refresh actions. |
| `web/components/sidebar.tsx` | Lazy directory tree, server-backed search, and tree virtualization. |
| `web/components/diff-panel.tsx` | Active entry loading and text/image/binary/symlink/issue presentation. |
| `web/components/editor-tabs.tsx` | Removable editor tab strip and active-file selection. |
| `web/lib/content-cache.ts` | Snapshot-bound browser LRU for entry detail and text content. |
| `workspace.test.ts` | Comparison semantics, filesystem safety, limits, progress, and cancellation. |
| `server.test.ts` | HTTP contracts, security headers, assets, refresh, and lifecycle. |
| `mcp.test.ts` | MCP protocol, tools, snapshot binding, stateless access, and Origin validation. |

## Architecture And Ownership

```text
CLI registration (index.ts)
  -> process composition (run.ts)
     -> ComparisonWorkspace (workspace.ts)
     -> DiffHttpServer (server.ts)
        -> browser HTTP adapter -> embedded React application (web/)
                              -> @pierre/diffs browser workers
        -> MCP adapter (/mcp) -> coding agents and AI clients
```

The ownership boundaries are intentional:

- `workspace.ts` owns comparison truth. It is the only layer that decides status, scope, content classification, and stale state.
- `server.ts` adapts workspace operations to HTTP. It validates untrusted request input but does not reimplement comparison rules.
- `mcp.ts` adapts snapshot operations to MCP tools. It validates protocol input and maps names, but does not construct paths or read files directly.
- `web/` owns navigation and presentation. It never constructs filesystem paths or infers a comparison status from file content.
- `types.ts` is the contract between the workspace and its callers. Prefer extending this contract over reaching into workspace internals.

The primary interfaces are `ComparisonWorkspace`, `RefreshRun`, and `ComparisonSnapshot` in `types.ts`. Entry IDs are opaque snapshot-local identifiers. Callers must not assume an ID survives refresh or can be converted into a path.

## Comparison Scope

A Comparison Path is an exact, case-sensitive, POSIX-style relative path. Different paths do not undergo rename or similarity detection. Directories organize the tree but do not receive independent statuses, so empty-directory-only changes are not reported. A case-only rename is Deleted plus Added on every platform.

Target Directory `.gitignore` files define the scope for both trees. Rules are hierarchical and support Git negation. Baseline-only subtrees inherit the nearest Target ancestor rules. `--no-gitignore` disables this policy, while repeated `--exclude` globs apply to both sides after `.gitignore` rules.

Hard exclusions always win at every depth:

```text
All platforms:
  .git

macOS:
  .DS_Store
  ._*
  .Spotlight-V100/
  .Trashes/

Windows, matched case-insensitively:
  Thumbs.db
  ehthumbs.db
  Desktop.ini
  $RECYCLE.BIN/
  System Volume Information/
```

Do not add dependencies, build output, editor metadata, or other project-specific paths to the hard exclusions. Those belong in `.gitignore` or explicit `--exclude` arguments.

## Filesystem Semantics

- Regular files are compared by original bytes. Timestamps and mode bits do not affect status.
- Same-size files are read in bounded chunks and stop at the first difference.
- Symbolic links are compared by their stored link target and are never traversed.
- Kind changes at one Comparison Path are Modified.
- Sockets, devices, FIFOs, unreadable entries, unreadable Target `.gitignore` files, and repeatedly mutating entries become visible Comparison Issues.
- Discovery and reads use recorded device, inode, size, mtime, and ctime fingerprints.
- Every captured source fingerprint and both fixed root identities are revalidated before snapshot publication.
- Content requests reopen by the snapshot-recorded path with no-follow behavior and revalidate the fingerprint before and after reading.
- A path that changed after snapshot publication returns stale data status instead of mixing current bytes with old metadata.

Keep these behaviors fail-closed. A filesystem uncertainty should become a Comparison Issue or stale response, never a guessed status or an unrestricted read.

## Snapshot Lifecycle

Snapshots are immutable, in-memory indexes. They contain paths, metadata, statuses, issues, directory aggregates, and source fingerprints, but not complete file contents or precomputed patches.

A refresh proceeds through `discovering`, `comparing`, and `publishing`:

1. Read Target ignore rules and record inaccessible scope as issues.
2. Discover Baseline and Target concurrently under the same scope.
3. Form and lexically sort the union of paths for deterministic IDs.
4. Classify one-sided entries and kind changes without reading file contents.
5. Compare matching regular files and symlink targets.
6. Build counts, issues, search data, and lazy tree indexes in private memory.
7. Publish the completed snapshot with one reference swap.

The previous snapshot remains readable while refresh runs. Cancellation or failure never partially replaces it. Publishing a new snapshot invalidates browser tabs, cached content, and prior MCP read references because every read is bound to a snapshot ID.

## Workspace Query Contract

`ComparisonSnapshot` exposes these operations:

| Operation | Behavior |
| --- | --- |
| `list(query)` | Filtered flat entries with opaque cursor pagination; maximum 500 per page. |
| `tree({ path })` | Immediate directory children plus descendant status aggregates. |
| `search(query, statuses, limit)` | Sorted file and folder matches; maximum 200 results. |
| `detail(entryId)` | Metadata and presentation classification for one entry. |
| `content(entryId, side, force)` | Decoded text subject to guarded and blocked limits. |
| `textDiff(entryId, options)` | Bounded, analysis-only Unified Diff generated through safe snapshot reads. |
| `blob(entryId, side)` | Original bytes for supported image presentation. |

When adding a query, keep it snapshot-local and bounded. Do not expose arbitrary paths through the workspace or HTTP interfaces.

## HTTP Interface

All API responses use `Cache-Control: no-store` and versioned error objects. Snapshot-bound routes require `snapshot=<id>` and return `409 SNAPSHOT_CHANGED` when that snapshot is no longer published.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/state` | Workspace phase, progress, and current snapshot summary. |
| `GET` | `/api/events` | Server-sent workspace state and progress updates. |
| `POST` | `/api/refresh` | Start an atomic refresh. |
| `DELETE` | `/api/refresh` | Cancel the active refresh. |
| `GET` | `/api/entries` | Bounded flat entry page with status/path/kind filters. |
| `GET` | `/api/tree` | Immediate children for one directory path. |
| `GET` | `/api/search` | Bounded file and folder search. |
| `GET` | `/api/entries/:id` | Entry detail and presentation type. |
| `GET` | `/api/entries/:id/content/:side` | Decoded text for Baseline or Target. |
| `GET` | `/api/entries/:id/blob/:side` | Original image bytes for Baseline or Target. |
| Streamable HTTP | `/mcp` | Stateless MCP tools over the same Comparison Workspace. |

The browser HTTP adapter does not generate patches. It sends text content to `@pierre/diffs` browser workers. The MCP adapter separately generates bounded Unified Diffs through `ComparisonSnapshot`. Bundled hashed assets are immutable-cacheable; the HTML shell remains `no-store`.

## MCP Client Integration

Start one long-running diff service for the directory pair the client should inspect:

```bash
ycy diff --port 1205 /absolute/path/to/baseline /absolute/path/to/target
```

Keep that process running. The printed `MCP endpoint` is the Streamable HTTP URL to register; with the default port it is `http://127.0.0.1:1205/mcp`. The roots cannot be changed through MCP. Start another service, normally on another port, for a different directory pair.

Register the local endpoint with a client that supports Streamable HTTP. For Codex CLI:

```bash
codex mcp add ycy-diff --url http://127.0.0.1:1205/mcp
```

For Claude Code, use an HTTP transport and choose the configuration scope explicitly:

```bash
claude mcp add --transport http --scope project ycy-diff http://127.0.0.1:1205/mcp
```

Clients that use the `.mcp.json` convention can use the equivalent project configuration:

```json
{
  "mcpServers": {
    "ycy-diff": {
      "type": "http",
      "url": "http://127.0.0.1:1205/mcp"
    }
  }
}
```

Configuration field names vary between clients; select the Streamable HTTP or HTTP transport, not stdio or the legacy SSE transport. No command, environment variable, bearer token, or OAuth setting is required.

After connecting, use this tool sequence:

1. Call `get_comparison` and retain the returned `snapshot_id`.
2. Discover work with `list_changes`, `search_changes`, or `list_issues` using that `snapshot_id`.
3. Pass an `entry_id` from a changed entry to `get_text_diff` when a textual explanation is needed.
4. Call `refresh_comparison` after the directories change and poll `get_comparison` until the phase is `ready`, `error`, or `canceled`. Replace cached Snapshot IDs, Entry IDs, and cursors only after a new snapshot is successfully published; an error or cancellation leaves the prior snapshot authoritative.

The server is stateless at the MCP transport layer, but its read references are snapshot-bound. A `snapshot_changed` error means the client must restart at `get_comparison`; `invalid_cursor` means it must restart that listing. `get_text_diff` can return an unavailable result for non-text, stale, oversized, too-complex, or capacity-limited content without making the whole comparison fail.

For access from another machine on a trusted local network, start the service with `--public` and register one of the printed `Network MCP` URLs:

```bash
ycy diff --public --port 1205 /absolute/path/to/baseline /absolute/path/to/target
```

`--public` exposes both the browser application and the complete read-only MCP workspace on `0.0.0.0` without authentication or TLS. Do not use it on an untrusted network or expose the port through a public firewall, tunnel, or router. Browser-origin MCP requests must be same-origin and use an address permitted by the active binding; normal agent clients may omit `Origin`.

## MCP Interface

The MCP endpoint is always available at `/mcp` on the diff HTTP server. It uses stateless Streamable HTTP with JSON responses, so clients do not receive or retain an MCP session ID. It publishes no resources, prompts, subscriptions, or progress stream; clients poll `get_comparison` after Refresh. The CLI prints the local endpoint and, with `--public`, every non-internal IPv4 endpoint.

The Comparison Workspace roots are fixed when `ycy diff` starts. No MCP input accepts an absolute path or chooses another directory. Call `get_comparison` first, then pass its `snapshot_id` to every read tool. A published replacement invalidates the old ID and every Entry ID from that snapshot.

| Tool | Contract |
| --- | --- |
| `get_comparison` | Return phase, error state, and the current snapshot summary when published. |
| `refresh_comparison` | Start an asynchronous atomic Refresh; concurrent calls return `already_running`. |
| `list_changes` | Cursor-page Added, Deleted, and Modified entries; filter by status, either-side kind, or path substring. Default 100, maximum 500. |
| `list_issues` | Cursor-page Comparison Issues separately from changes. Default 100, maximum 500. |
| `search_changes` | Case-insensitive Comparison Path substring search over changed entries. Default 20, maximum 100; returns `truncated` instead of a cursor. |
| `get_text_diff` | Generate an on-demand Text Difference from `snapshot_id` plus opaque `entry_id`. Context defaults to 3 and accepts 0 through 20. |

Tool results use `structuredContent` with snake-case field names and a short text summary. A Comparison Entry exposes independent `baseline` and `target` Entry States. A file state contains `size`; a symlink state contains `link_target`. Unchanged entries are summary-only and cannot be listed or searched through MCP.

Snapshot and cursor failures are tool errors with structured `snapshot_changed` or `invalid_cursor` codes. An unknown or non-change Entry ID passed to `get_text_diff` returns `entry_not_found`. Input type and range errors use MCP parameter validation.

MCP Unified Diffs are for analysis, not guaranteed application by Git or `patch`. Headers are fixed to `baseline`, `target`, or `/dev/null`, never a Comparison Path. Whitespace changes are retained. Normal text limits still apply; guarded content cannot be forced through MCP. Complete patch output is capped at 256 KiB, calculation at 5 seconds, and concurrent calculations at two. Unavailable results distinguish `non_text`, `mixed_entry_kinds`, `source_too_large`, `stale`, `complexity_limit`, `output_too_large`, and `server_busy`.

## Content Presentation

Presentation classification is lazy and happens only when an entry is opened:

- UTF-8, UTF-8 BOM, UTF-16 LE BOM, and UTF-16 BE BOM are text.
- Invalid or unsupported text encodings are binary.
- `.avif`, `.gif`, `.jpeg`, `.jpg`, `.png`, `.svg`, and `.webp` use image presentation.
- Symlinks show their stored targets.
- Comparison Issues and stale entries have dedicated states.
- Modified text that decodes identically on both sides reports an encoding/BOM-only change.

Text limits apply independently to each side:

| Level | Size | Lines | Behavior |
| --- | ---: | ---: | --- |
| Normal | Up to 2 MiB | Up to 50,000 | Load automatically. |
| Guarded | Up to 20 MiB | Up to 200,000 | Require an explicit user action. |
| Blocked | Over either guarded limit | Any | Show metadata without rendering text. |

A single decoded line over 1 MiB is always Blocked.

## Web Application Invariants

- The sidebar loads directory children lazily and virtualizes visible rows.
- File and folder search is server-backed, debounced, status-aware, and capped at 200 results.
- Several tabs may remain open, but only the active file mounts a diff renderer.
- The active diff fills the remaining editor area and owns a compact status bar for path, status, and size transition.
- Below 900 px, navigation moves into a Sheet and text diffs use unified mode. The stored desktop mode is restored above that breakpoint.
- `@pierre/diffs` owns text hunk calculation, syntax highlighting, split/unified rendering, and line virtualization.
- The worker pool is capped at four workers.
- Entry detail and text content use a 32 MiB snapshot-keyed LRU. Snapshot changes clear the cache and open tabs.
- Theme, split/unified mode, wrapping, ignore-whitespace rendering, and desktop panel layout may be stored locally. Filesystem paths and contents must not be persisted by the browser.
- Ignore-whitespace changes rendered hunks only. It must never change server status or summary counts.

## Security Invariants

- Default binding is `127.0.0.1`. `--public` explicitly binds `0.0.0.0` for an unauthenticated LAN share and prints every non-internal IPv4 URL.
- `/mcp` follows the same binding and authorization decision as the browser interface; it does not add authentication.
- Do not add permissive CORS headers.
- MCP requests with an `Origin` header require an exact same-origin match and a hostname permitted by the active binding. Agent requests without `Origin` remain valid.
- Refresh mutations reject a non-matching `Origin`.
- Content routes accept only a snapshot ID, opaque entry ID, and side enum.
- No upload, edit, merge, delete, or arbitrary-path route belongs in this server.
- Keep `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, and the restrictive Content Security Policy.
- Inline only the image MIME allowlist. SVG responses require their sandboxing CSP.
- Use React text nodes and structured JSON for filenames and errors; do not concatenate untrusted values into HTML.
- The embedded app must not require a CDN, remote font, analytics endpoint, or remote source map.

## Performance Boundaries

The design target is 100,000 included entries and roughly 20 GiB per side. These constants protect responsiveness and memory use:

| Boundary | Current value |
| --- | ---: |
| Concurrent directory reads | 16 |
| Concurrent comparisons | 8 |
| Progress publication interval | 250 ms |
| Entry page maximum | 500 |
| Search result maximum | 200 |
| MCP search result maximum | 100 |
| MCP Unified Diff output | 256 KiB |
| MCP Unified Diff calculation | 5 seconds, 2 concurrent |
| Browser content cache | 32 MiB |
| Browser diff workers | Up to 4 |

Memory should remain proportional to included entry count, not total file bytes. The server loads content only for an explicit request; the browser loads content only for the active editor. Preserve lazy tree queries, bounded pages, worker-backed diffing, and cancellation when extending the module.

## Design Decisions

The former ADRs are consolidated here:

1. **Target `.gitignore` controls both trees.** The comparison is directional. Applying each tree's rules independently can create false Added or Deleted entries when ignore files differ.
2. **Text rendering uses `@pierre/diffs`.** It provides React 19 support, split/unified modes, virtualization, Shiki highlighting, wrapping, and portable workers. The tradeoff is a larger embedded frontend and dependency coupling.
3. **Snapshots are immutable and in memory.** Metadata and indexes stay resident while file contents load lazily. This avoids a comparison database and precomputed patch storage; process restart discards the snapshot.
4. **The server binds to loopback by default.** Its API exposes source contents without authentication, so `--public` is required before sharing it on the local network.

## Extending The Module

Use these paths to keep changes inside the correct owner:

- **New comparison status:** update `types.ts`, workspace classification/counts/tree aggregation, server validation, browser API types, status filters, styles, and workspace/server tests.
- **New presentation type:** update `EntryPresentation`, workspace detail/content/blob behavior, HTTP response handling, `web/api.ts`, and `diff-panel.tsx`.
- **New bounded query:** add it to `ComparisonSnapshot`, implement it against immutable indexes, validate it in `server.ts`, then add a browser helper and UI consumer.
- **Ignore-policy change:** keep all matching in `workspace.ts`; add exact tests for Target/Baseline asymmetry, nested rules, negation, and hard exclusions.
- **New toolbar preference:** keep server semantics unchanged, use an accessible icon control, persist only display state, and ensure mobile hiding does not leave orphaned separators.
- **Refresh behavior change:** preserve the prior published snapshot until the replacement is complete and ensure every fetch remains snapshot-bound.

Avoid adding a new abstraction unless it removes meaningful complexity or matches an existing ownership boundary. In particular, do not introduce filesystem paths into the web layer, HTTP concerns into the workspace, or diff-presentation rules into the server.

## Verification Expectations

Tests are colocated with the module under `src/commands/diff`.

Workspace tests should cover observable semantics through `ComparisonWorkspace`: statuses, byte equality, nested ignore rules, explicit exclusions, hard exclusions, symlinks, unsupported filesystem entries, encodings, limits, mutation retries, stale reads, progress, cancellation, and atomic publication.

Server tests should cover route validation, snapshot conflicts, pagination, tree/search queries, SSE, same-origin refresh, content disposition, security headers, embedded assets, cancellation, and graceful shutdown.

For frontend changes, verify both desktop and sub-900 px layouts, light and dark themes, active/inactive toggle states, long paths, empty states, and console errors. Keep only one mounted diff renderer regardless of the number of open tabs.

## Non-goals

- Editing, copying, deleting, merging, or downloading files through the UI.
- Git commits, branches, staging, or rename detection.
- Following symbolic links.
- Real-time filesystem watching.
- Hex diffs for arbitrary binaries.
- Persistent snapshots or a comparison database.
- Authentication or internet-facing deployment.
