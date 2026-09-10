# Frozen Bun Archive

This directory is the read-only implementation reference captured from commit
`78358c0201b71891e36603d6abb8d7c87d54ad57`. It is not part of the active
build, test, hook, packaging, or runtime path.

## Source Baseline

- Source commit: `78358c0201b71891e36603d6abb8d7c87d54ad57`
- Root package version: `0.0.69`
- Bun version: `1.3.14`
- Capture date: `2026-08-23`

The pre-move baseline was recorded without repairing legacy behavior:

| Probe | Result |
| --- | --- |
| `bun test` | passed: 357 pass, 1 Windows-only skip, 0 fail |
| `bun run typecheck` | passed |
| `bun run lint` | passed |
| `bun run build` | passed |

## Archive Ledger

`MANIFEST.tsv` is the source of truth for every archived tracked file. Its
columns are `original_path`, `archive_path`, `git_mode`, and `git_blob`.
The manifest excludes this evidence file and itself because both are cutover
metadata rather than source files from the frozen commit.

The archive contains the exact cutover ledger:

- `src/**`
- `scripts/build.ts`, `scripts/evaluate-git-cm.ts`,
  `scripts/generate-frp-manifest.ts`, `scripts/install-tunnel-frp.ts`, and
  `scripts/prepare-seven-zip.ts`
- root Bun package metadata and configuration
- `.github/workflows/**`, `Dockerfile`, `.dockerignore`, and `deploy/**`
- the original root `README.md`

The active tree retains `scripts/install.sh`, `scripts/install.ps1`,
`LICENSE`, `CLAUDE.md`, `public/**`, `mock/**`, and `.github/logo.*`.

## Frontend Copy Provenance

The active Vite sources began as byte-identical copies of these archive paths:

| Archive source | Active destination |
| --- | --- |
| `legacy/bun/src/commands/diff/web/**` | `web/diff/**` |
| `legacy/bun/src/commands/fs/web/**` | `web/fs/**` |
| `legacy/bun/src/commands/tunnel/server/web/**` | `web/tunnel-server/**` |
| `legacy/bun/src/shared/web/**` | `web/shared/**` |

Subsequent changes in `web/` are Vite adaptation only. The archive-side files
remain unchanged.

## Reproducible Checks

From the repository root, compare the manifest to the Git index after staging
the cutover. Every row must retain the source path, archive path, mode, and
blob ID exactly:

```sh
diff -u \
  <(tail -n +2 legacy/bun/MANIFEST.tsv | LC_ALL=C sort) \
  <(git ls-files -s -- legacy/bun | awk '$4 != "legacy/bun/ARCHIVE.md" && $4 != "legacy/bun/MANIFEST.tsv" { archive = $4; original = substr(archive, 12); printf "%s\\t%s\\t%s\\t%s\\n", original, archive, $1, $2 }' | LC_ALL=C sort)
```

Before Vite adaptation, the four source/destination pairs above compare with
`diff -qr`. At cutover review, inspect the staged patch for `web/` to confirm
that only Vite/package-local adaptations follow those copies. The active test
suite must never execute the archived Bun implementation.
