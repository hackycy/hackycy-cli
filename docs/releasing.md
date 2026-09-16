# ycy Release Runbook

The release baseline is recorded in `cmd/ycy/VERSION` and published as an
annotated Git tag with a `v` prefix. The initial migrated value is `0.0.69`;
historical `v0.0.x` Bun releases remain unchanged. Maintainers do not edit
`cmd/ycy/main.go` for a release.
This release line does not use macOS or Windows code signing; integrity is
provided by the SHA-256 manifest published with the binaries.

## Version Policy

Release tags must be annotated, point to a commit reachable from `main`, and
match `vX.Y.Z` exactly. Pre-release (`rc`, `beta`, and similar), build metadata,
the `v`-less form, and numeric components with leading zeroes are rejected.

Use [Conventional Commits](https://www.conventionalcommits.org/) for commit
messages. Increment MAJOR for incompatible user-facing contracts, MINOR for
backward-compatible features, and PATCH for fixes. GitHub generates release
notes; this repository intentionally does not maintain a `CHANGELOG` file.

## Before Tagging

Run the release target from the repository root on a clean `main` checkout:

```sh
make release
```

The Go tool requires the `main` branch and an interactive terminal. It
fast-forwards from `origin`, reads `cmd/ycy/VERSION`, and offers major, minor,
patch, next, and Conventional Commit-derived candidates. `next` is the
default patch candidate. After a summary confirmation it runs `make check`,
updates VERSION, creates and pushes a `chore(release): vX.Y.Z`
commit to `main`, then creates and pushes the annotated tag. The tag push starts
`.github/workflows/release.yml`, which builds the release assets and publishes
the GitHub Release. Run the Docker workflow manually from that release tag after
the Release is public.

To run the same preflight without creating or pushing a tag:

```sh
DRY_RUN=1 make release
```

The tool rejects prereleases, build metadata, leading zeroes, version drift,
existing local or remote tags, dirty trees, non-`main` branches, and
non-fast-forward updates. It never force-pushes. Dry-run performs the pull,
selection, confirmation, and checks but does not write VERSION, commit, create
a tag, or push. If the release commit push fails, the local commit is kept for
diagnosis. If the final tag push fails, the local annotated tag is kept for an
exact retry; do not delete or replace a published Release.

## Pipeline Contract

The release workflow has two jobs. The first checks that the selected ref is an
annotated stable tag matching `cmd/ycy/VERSION`, then uses a fresh checkout to
run:

```sh
make release-candidate
```

The candidate reads the version from `cmd/ycy/VERSION` and contains the six
long-standing bare binary names and `SHA256SUMS`. The verifier checks target
format, CGO-free builds, Go build metadata, Web and 7-Zip embedding, FRP
manifest values, and checksums. The second job downloads those assets and
creates the public GitHub Release with generated notes.

Docker is a separate manual workflow. Select the release tag when starting it;
it downloads and verifies the two Linux assets, runs
`tools/prepare-frp-runtime` to materialize the manifest-pinned FRP 0.70.1 pair,
and builds `linux/amd64,linux/arm64` images.

## Recovery

- **Build failure:** rerun the Release workflow for the same tag after fixing
  the build issue.
- **Docker failure after publication:** run the Docker workflow manually with
  the release tag selected in GitHub Actions.

There is no supported replacement flow for a published Release because GitHub
Immutable Releases are enabled.

## Consumer Verification

Verify a downloaded binary against the release manifest:

```sh
sha256sum -c SHA256SUMS
./ycy-linux-x64 --version
```

The installer and `ycy upgrade` continue to use the six existing names and the
`SHA256SUMS` fallback. Published image references are:

```text
ghcr.io/hackycy/hackycy-cli:vX.Y.Z
ghcr.io/hackycy/hackycy-cli:latest
sgccr.ccs.tencentyun.com/sooosin-sg/hackycy-cli:vX.Y.Z
sgccr.ccs.tencentyun.com/sooosin-sg/hackycy-cli:latest
```

## Required Repository Settings

Maintainers must configure:

- a `v*` tag ruleset requiring annotated tags and protecting the release
  workflow;
- GitHub Immutable Releases;
- Actions permissions that allow read-only checkout, release contents write,
  and package write;
- repository secrets `TENCENT_USERNAME` and `TENCENT_PASSWORD`.

Third-party Actions track their stable major-version tags. `.github/dependabot.yml`
checks for weekly updates.

## References

- [GitHub CLI release commands](https://cli.github.com/manual/gh_release)
- [GitHub Immutable Releases](https://docs.github.com/en/repositories/releasing-projects-on-github/immutable-releases)
- [GitHub Actions workflow syntax](https://docs.github.com/en/actions/using-workflows/workflow-syntax-for-github-actions)
- [Go build command](https://pkg.go.dev/cmd/go#hdr-Compile_packages_and_dependencies)
- [GoReleaser](https://goreleaser.com/) (studied for release conventions; not
  used because it cannot own this project's Web, 7-Zip, FRP, and updater asset
  contract)
