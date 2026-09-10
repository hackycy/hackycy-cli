# File Browser Command

This directory contains `ycy fs`: a root-confined filesystem workspace, a same-origin HTTP adapter, and an embedded React file browser.

## Purpose

```text
ycy fs [options] [directory]

Options:
  -p, --port <number>       Port for the file browser (default: 1204)
  -a, --address <string>    Address to bind to (default: 0.0.0.0)
  -m, --manage              Enable uploads, downloads, extraction, and filesystem management
      --chunked-upload      Enable chunked uploads for files larger than 20 MiB
      --upload-chunk-size <MiB>
                             Chunk size for large uploads (4-16 MiB, default: 8)
      --safe-html           Disable HTML and XHTML execution and force downloads
      --account <user:pass> Require login with an account (repeatable)
      --session-dir <path>  Persistent directory for authenticated sessions
      --session-idle-days <days>
                             Session idle lifetime in days (default: 7)
```

The directory defaults to the current working directory. The default binding is available to the local network; use `--address 127.0.0.1` for local-only access. Management mode allows text editing, upload, remote download, archive extraction, copy, move, rename, and permanent deletion. Without `--account`, use it only with a trusted directory and network.

When `--chunked-upload` is enabled, browser uploads larger than 20 MiB use sequential 8 MiB chunks by default. The chunk size may be set from 4 to 16 MiB with `--upload-chunk-size`; `YCY_FS_CHUNKED_UPLOAD` and `YCY_FS_UPLOAD_CHUNK_SIZE_MIB` provide the equivalent container configuration. Chunked uploads do not impose the legacy 1 GiB multipart limit. The server checks the target filesystem before creating a session, reserves the declared size across active sessions, and preserves 10% of free space up to 1 GiB. Upload sessions are process-local, expire after 30 minutes of inactivity, and are removed on normal shutdown; refreshing the page or restarting the process does not resume a selected browser file.

HTML and XHTML files are executable same-origin pages by default, similar to a conventional static file server. Use `--safe-html` to sandbox those documents and force them to download instead. This option does not remove a sandbox imposed by an outer iframe; the embedding page must grant `allow-scripts`.

Passing one or more accounts enables login mode:

```bash
ycy fs --account 'alice:password123' --account 'bob:another-password' ./shared
```

The first colon separates the username from the password, so the password may contain additional colons. Usernames contain 1-64 ASCII letters, numbers, dots, underscores, or hyphens and are matched without case sensitivity. Passwords contain 5-256 characters. Duplicate usernames stop startup. Account specifications are process arguments and may be visible in shell history and process inspection tools.

When accounts are configured, sessions are stored outside the browsed directory. `--session-dir` takes precedence over `YCY_FS_SESSION_DIR`; otherwise ycy uses a platform state directory isolated by the resolved browse root. `--session-idle-days` takes precedence over `YCY_FS_SESSION_IDLE_DAYS`; both default to seven days.

## Architecture

```text
CLI registration (index.ts)
  -> process composition (run.ts)
     -> FsWorkspace (workspace.ts)
     -> FsAuthentication (authentication.ts, when accounts are configured)
     -> FsHttpServer (server.ts)
        -> JSON and file HTTP adapter
        -> ChunkedUploadManager with bounded process-local sessions (when enabled)
        -> RemoteDownloadManager with a bounded process-local queue
        -> ExtractionManager with a single-worker process-local queue
        -> ThumbnailService and two persistent conversion workers
        -> embedded React application (web/)
```

- `workspace.ts` owns root confinement, symlink policy, metadata, text decoding, and atomic upload naming. Callers pass only POSIX relative paths.
- `authentication.ts` owns account parsing, password hashing, and the account adapter for the shared persistent file-session module.
- `server.ts` maps workspace results to HTTP, validates methods and mutation origins, implements cache and Range semantics, and serves the embedded HTML bundle.
- `chunked-upload-service.ts` owns bounded upload-session state, caller isolation, ordered offsets, lifecycle cleanup, and completion idempotency. `workspace.ts` owns capacity reservation, destination-local staging, stream writes, and atomic publication.
- `download-service.ts` validates remote HTTP(S) targets, blocks literal private and reserved IP addresses, follows validated redirects, and owns the bounded process-local download queue.
- `archive-extractor.ts` owns 7-Zip inspection, capacity checks, multi-layer TAR extraction, process cancellation, and normalized failures. `workspace.ts` owns staging, output validation, collision-safe naming, and atomic publication. `extraction-service.ts` owns the bounded single-worker queue.
- `thumbnail-service.ts` owns input limits, request coalescing, the bounded worker queue, and the session-only LRU. `thumbnail-worker.ts` performs WASM decoding and WebP conversion off the HTTP thread.
- `web/` owns directory History navigation, sorting, virtualization, preview state, theme, one syntax-highlighting worker, and the three-worker upload queue. It never constructs absolute filesystem paths.
- Shared Radix/Tailwind primitives live under `src/shared/web` and are consumed by both `fs` and `diff`.

## HTTP Interface

| Route | Behavior |
| --- | --- |
| `GET /`, `GET /browse/*` | Embedded React application and browser history fallback. |
| `GET\|POST\|DELETE /api/session` | Inspect the current login state, authenticate, or end the current session. |
| `GET /api/directory?path=` | Current directory metadata and entries. |
| `GET\|PUT /api/text?path=` | Read text previews with SHA-256 revisions or conditionally save text in management mode. |
| `POST /api/upload?path=` | One multipart file per request when `--manage` is enabled. |
| `POST /api/uploads` | Create a large-upload session when chunked uploads are enabled. |
| `GET /api/uploads/:id` | Inspect a chunked-upload session's confirmed offset. |
| `PUT /api/uploads/:id` | Append one ordered `Content-Range` chunk. |
| `POST /api/uploads/:id/complete` | Atomically publish a completed chunked upload. |
| `DELETE /api/uploads/:id` | Cancel a chunked-upload session. |
| `POST /api/operations` | Validated create-directory, rename, copy, move, and permanent-delete commands in management mode. |
| `GET\|POST\|DELETE /api/downloads` | List, create, or clear terminal remote-download tasks in management mode. |
| `GET /api/downloads/events` | Server-sent task snapshots for remote-download progress. |
| `POST /api/downloads/:id/cancel` | Cancel one queued or active remote download. |
| `POST /api/downloads/:id/retry` | Retry one failed or cancelled remote download as a new task. |
| `GET\|POST\|DELETE /api/extractions` | List, enqueue up to 100 archive paths, or clear terminal extraction tasks in management mode. |
| `GET /api/extractions/events` | Server-sent task snapshots for extraction progress. |
| `POST /api/extractions/:id/cancel` | Cancel one queued or active extraction. |
| `POST /api/extractions/:id/retry` | Retry one failed or cancelled extraction as a new task. |
| `GET\|HEAD /files/*` | Original file bytes; `?download=1` forces attachment. |
| `GET\|HEAD /thumbnails/*` | 160×160 WebP thumbnail for JPEG, PNG, WebP, AVIF, or GIF input. |

The former direct file URL shape is intentionally not retained. A file browser path such as `docs/readme.txt` is available at `/files/docs/readme.txt`; `/browse/docs` is the browser route.

Errors use `{ version: 1, error: { code, message } }`. Chunk-session creation uses JSON; every `PUT /api/uploads/:id` must contain `application/octet-stream`, an exact `Content-Range: bytes start-end/total`, and a matching content length. Chunks are sequential per session, and callers recover a transport failure by reading the confirmed offset before retrying. Directory and text responses are not cacheable. Original files support ETag, Last-Modified, HEAD, and one byte range. Thumbnail responses support ETag, Last-Modified, and conditional 304 responses. Only `/files/*` enables wildcard CORS, and only when login mode is disabled.

## Text Editing

- A file is text-capable when the bounded bytes returned by the text reader decode as UTF-8, BOM-marked UTF-16 LE, or BOM-marked UTF-16 BE. This is a content capability, not an extension or MIME allowlist. The browser probes text after opening files that are not already classified as image, video, audio, or PDF, so extensionless files such as `.claude` can become text previews without making directory listings read every file.
- The existing `GET /api/text?path=` response includes an opaque SHA-256 `revision` on `ready` results. `PUT /api/text?path=` accepts a UTF-8 `text/plain` request body and requires `If-Match` with that revision. Missing preconditions return `428`; a stale revision returns `412` and never overwrites the file. Successful responses return the new revision, byte size, modification time, and encoding.
- Editing is exposed only in management mode, after the current preview is `ready`, and only when the final directory entry is a regular file. Internal symlinks remain previewable and downloadable but are not edit targets. Requests repeat root, regular-file, supported-encoding, size, and revision checks independently of the UI.
- The editor enters from the preview header in a full-screen dialog and uses Monaco Editor with bundled same-origin language workers. Common recognized languages get Monaco language support; unknown or unsupported languages use a plain-text editor. Save, Cancel, dirty/saving/conflict states, Ctrl/Cmd+S, and dirty-navigation guards are required. Edit state and drafts remain in page memory and do not enter the preview URL or browser History.
- On save, the server preserves the original encoding and BOM, converts all line endings to the source file's dominant style (LF wins ties), preserves whether the file ended with a newline, and rejects output over 10 MiB. It preserves mode bits, writes a temporary file beside the target, and atomically replaces the original only after a final revision check. A per-path process-local save lock serializes in-process writers; the narrow external TOCTOU window is documented residual risk.
- A `412` conflict keeps the local draft in the editor and offers Reload remote or Download draft. There is no force-overwrite action, system-clipboard draft copy, new-file creation, or save-as flow in this scope.

## Authentication Invariants

- Login mode is enabled by the presence of at least one `--account`. Without accounts, the HTTP behavior remains unauthenticated and `/api/session` reports that authentication is disabled.
- The application shell and its compiled assets remain public so the browser can render the login form. Every other `/api/*`, `/files/*`, and `/thumbnails/*` request requires a valid session.
- Passwords are hashed with Argon2id during startup. Failed logins use the same verification path whether or not the username exists.
- Sessions use opaque 256-bit random IDs in an `HttpOnly`, `SameSite=Strict` cookie. The server stores only a SHA-256 ID hash, subject, credential revision, and idle expiry in a protected session directory; raw IDs are never persisted.
- A session expires after seven days without an authenticated request and has no absolute maximum age. Successful authenticated responses refresh the persistent cookie and its file record, so a valid active session survives server and browser restarts. The application also refreshes its session daily while open.
- Logging out, expiry, bounded-session eviction, or changing an account password in the startup configuration revokes the token and closes its active remote-download event stream. Sessions remain bounded to eight per account and 128 for the server.
- The session directory allows one active fs server, uses restrictive file permissions, and is sensitive deployment data. Authentication does not add TLS; passwords and session cookies still travel over the HTTP connection exposed by `fs`.
- All configured accounts have the same read and management permissions. There is no registration, role, password-change, or persistent account interface.

## Filesystem And Management Invariants

- The root is resolved once at startup. Absolute paths, backslashes, dot segments, malformed URL encoding, and paths whose real target escapes the root are rejected.
- Internal symlinks may be followed. Escaping, unreadable, or unsupported entries are listed as unavailable without exposing their targets.
- Text preview checks the 10 MiB size limit before reading and treats invalid supported encodings as binary.
- Thumbnail input is capped at 64 MiB and 50 million pixels. SVG is never sent to the raster converter. Failed or oversized thumbnails remain file icons in the main browser and never trigger an original-image fallback.
- Thumbnail conversion uses two persistent workers, at most 128 queued tasks, a five-second task timeout, and replacement of a timed-out worker. Concurrent requests for the same file revision share one conversion.
- Thumbnail output stays in a process-local LRU keyed by path, size, and modification time. The cache holds at most 1000 entries or 32 MiB, writes nothing to the file browser directory, and is discarded when the server stops.
- Direct multipart uploads remain capped at 1 GiB, written to a temporary file in the destination directory, then published atomically with a hard link. When chunked uploads are enabled, browser files larger than 20 MiB use the bounded session protocol and are limited by filesystem capacity rather than an artificial file-size cap.
- Chunked-upload sessions use random IDs and are bound to the authenticated cookie session when login is enabled. At most three active sessions belong to one caller and at most 100 upload sessions (including retained terminal results) belong to the server. A session accepts at most one 4-16 MiB chunk at a time; a stalled chunk is aborted after five minutes. Sessions expire after 30 inactive minutes, and a completed result is kept for five minutes so completion can be retried safely.
- Before staging a chunked upload, the workspace reserves the declared full file size across all active sessions on the target filesystem. It leaves 10% of reported free space available, capped at 1 GiB. Cancellation, expiration, and normal shutdown remove the hidden destination-local temporary file; a publication failure keeps the session available for retry until it expires. A later upload in that directory removes stale temporary files left by abnormal termination.
- Remote downloads have no upload-size cap. Response bodies are streamed through bounded chunks into hidden destination-local temporary files, then published atomically with the same collision-safe naming rules as uploads.
- At most two remote downloads run concurrently, at most 100 wait in the queue, and terminal task records are pruned to a bounded history. Cancelling a task or stopping the server aborts the request and removes its temporary file.
- Remote download URLs permit only HTTP(S) and cannot contain credentials. Literal loopback, private, link-local, multicast, documentation, and other reserved IP addresses are rejected without resolving domain names. Every redirect is revalidated and the chain is capped at five hops.
- Remote-download state is process-local. Browser reloads recover active tasks through the list and event interfaces; process restarts do not resume tasks or retain their history.
- Archive extraction uses official 7-Zip 26.02. Release builds embed the matching `7zz`, or `7z.exe` and `7z.dll`, plus the complete upstream `License.txt`; files are SHA-256 verified before build and before release into the versioned application state directory.
- Source mode falls back to `7zz` or `7z` in `PATH` when no embedded runtime exists. Supported archive names include 7z, ZIP, RAR, TAR, gzip, bzip2, XZ, Zstandard, CAB, ARJ, LZH/LHA, CPIO, and compressed TAR variants.
- Extraction inspects archives before writing and rejects encryption and multipart archives. Path confinement relies on the pinned 7-Zip runtime's default extraction rules without `-spf` or `-spf2`; entry names are not rewritten for cross-platform separator consistency. The workspace still rejects unsafe links and special filesystem entries after extraction, checks available bytes and inodes, stages beside the source, preserves the archive, and atomically publishes to a collision-safe same-name directory.
- Archive extraction uses 7-Zip's default symbolic-link policy. Versioned link chains used by some macOS `.app` Framework bundles are not a compatibility guarantee and may cause extraction to fail.
- Exactly one archive is extracted at a time. At most 100 tasks wait and at most 100 task records are retained. Cancellation or server shutdown terminates the child process and removes staging content; old hidden staging directories are removed on a later extraction after 24 hours.
- Existing names are never overwritten. Collisions receive `name (1).ext` through `name (9999).ext`.
- The file browser root cannot be renamed, moved, copied, or deleted. Directories cannot be copied or moved into themselves.
- Rename and move reject collisions. Copy and upload choose collision-safe numbered names. Recursive copy does not dereference symbolic links.
- Delete is permanent and operates on the final directory entry itself, so deleting a symbolic link never deletes its target.
- All management requests require an exact same Origin and a hostname permitted by the active binding. Batch operations accept at most 1000 paths and report partial results per item.

## Web Application Invariants

- The application resolves `/api/session` before mounting the file browser. Authentication failures from JSON, upload, or event-stream requests return it to the login form.
- Directory URLs use `/browse/<encoded-path>` and browser History. Preview selection uses the `preview` query parameter but always replaces the current URL, so opening, switching, and closing previews never create History entries.
- List and grid views virtualize fixed-size rows with `@tanstack/react-virtual`, rendering four extra list rows or one extra grid row around the viewport. Grid columns are measured before entries mount and only change at layout breakpoints. Directories remain ahead of files for every sort. Search filters only the loaded directory.
- Main-list image elements load only `thumbnailUrl`, with lazy asynchronous low-priority decoding. Original `fileUrl` bytes are reserved for preview, opening, and download.
- Selection follows desktop file-manager conventions: clicking a file selects and previews it, Ctrl/Cmd toggles, Shift selects a range, and modifiers never open a preview. Directories open on double-click or Enter.
- Cut, copy, and paste use an in-memory browser clipboard. It survives directory navigation but not a page reload and never reads or writes the system clipboard.
- Source code, configuration files, and dotenv variants use the `@pierre/diffs` Shiki-based read-only file renderer. Plain text remains escaped content; HTML and XML are never executed in the preview. Opening an HTML/XHTML `fileUrl` in a new tab executes scripts by default; `--safe-html` downloads it instead.
- Images retain an inline preview and use `react-photo-view` for a current-image-only full-viewport viewer with pan, zoom, rotate, and reset controls. Audio, video, and PDF keep browser-native presentation.
- Theme, view mode, sort key, and direction may be stored locally. Paths and file contents are not persisted.
- Multi-file upload runs at most three files concurrently and refreshes the current listing when the queue settles. With chunking enabled, each large file sends sequential configured-size chunks, recovers transient transport failures from the server-confirmed offset with three exponential-backoff retries, and exposes cancellation in the activity center.
- Remote-download tasks share the activity center with uploads and file operations. They show transferred bytes, known percentage, server-measured speed, cancellation, and retry controls; unknown response lengths use an indeterminate progress bar.
- Extraction tasks use the same activity center and show waiting, inspection, progress, completion destination, cancellation, and retry states. Completing an extraction beside the visible directory refreshes its listing.

## Verification

```bash
bun test src/commands/fs
bun run test:fs:performance
bun test src/commands/diff
bun run typecheck
bun run lint --no-cache
bun scripts/build.ts --outfile .tmp/ycy
```

Changes to shared Web primitives must run both command suites. Changes to the HTML entrypoint must work from source and from the compiled standalone executable.
