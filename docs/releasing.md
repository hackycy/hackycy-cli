# ycy Release Runbook

The Go release version is the annotated Git tag. There is no release-version
file and maintainers do not edit `cmd/ycy/main.go` for a release. The first Go
release is `v0.1.0`; historical `v0.0.x` Bun releases remain unchanged.
This release line does not use macOS or Windows code signing; integrity is
provided by SHA-256 manifests and GitHub Artifact Attestations.

## Version Policy

Release tags must be annotated, point to a commit reachable from `main`, and
match `vX.Y.Z` exactly. Pre-release (`rc`, `beta`, and similar), build metadata,
the `v`-less form, and numeric components with leading zeroes are rejected.

Use [Conventional Commits](https://www.conventionalcommits.org/) for commit
messages. Increment MAJOR for incompatible user-facing contracts, MINOR for
backward-compatible features, and PATCH for fixes. GitHub generates release
notes; this repository intentionally does not maintain a `CHANGELOG` file.

## Before Tagging

Run the complete local gate from a clean `main` checkout:

```sh
git switch main
git pull --ff-only origin main
make check
actionlint .github/workflows/release.yml .github/workflows/docker.yml
git tag -a v0.1.0 -m "chore: release v0.1.0"
git push origin v0.1.0
```

The tag push starts `.github/workflows/release.yml`. A manual dispatch with the
same `tag` input is available for a failed run while the Release is still a
draft.

## Pipeline Contract

The workflow validates the tag and release state, runs `make bootstrap && make
check`, then uses a fresh checkout to run:

```sh
make release-candidate RELEASE_VERSION=X.Y.Z
```

The candidate contains the six long-standing bare binary names and
`SHA256SUMS`. The verifier receives the version explicitly and checks target
format, CGO-free builds, Go build metadata, Web and 7-Zip embedding, FRP
manifest values, and checksums. `actions/attest` creates GitHub Artifact
Attestations for the six binaries from that manifest.

Publish creates a draft Release with generated notes, downloads the remote
assets again, requires exactly the seven-file asset set, reruns
`sha256sum -c SHA256SUMS`, and runs the Linux amd64 `--version` check. Only then
is the draft made public. A failed check deliberately leaves the draft for
diagnosis. A rerun deletes and recreates an unpublished draft; an existing
published Release always fails validation and is never overwritten.

Docker runs only after publication. It downloads and verifies the two Linux
assets, runs `tools/prepare-frp-runtime` to materialize the manifest-pinned FRP
0.70.1 pair for both architectures, and builds BuildKit-provenanced
`linux/amd64,linux/arm64` images.

## Recovery

- **Quality or build failure:** rerun the Release workflow for the same tag.
- **Attestation failure:** rerun after fixing repository Actions permissions;
  the draft has not been published.
- **Remote checksum or self-check failure:** inspect the draft and rerun. Do
  not publish or replace assets manually.
- **Docker failure after publication:** run the Docker workflow manually with
  `tag=vX.Y.Z`. It verifies that the Release is public before pushing images.

There is no supported replacement flow for a published Release because GitHub
Immutable Releases are enabled.

## Consumer Verification

Verify a downloaded binary against the release manifest:

```sh
sha256sum -c SHA256SUMS
gh attestation verify ycy-linux-x64 --repo hackycy/hackycy-cli
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
  package write, and artifact attestation (`id-token: write` and
  `attestations: write` only in the attestation job);
- repository secrets `TENCENT_USERNAME` and `TENCENT_PASSWORD`.

Third-party Actions are pinned to full commit SHAs and `.github/dependabot.yml`
checks for weekly updates.

## References

- [GitHub CLI release commands](https://cli.github.com/manual/gh_release)
- [GitHub Artifact Attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations)
- [GitHub Immutable Releases](https://docs.github.com/en/repositories/releasing-projects-on-github/immutable-releases)
- [GitHub Actions workflow syntax](https://docs.github.com/en/actions/using-workflows/workflow-syntax-for-github-actions)
- [Go build command](https://pkg.go.dev/cmd/go#hdr-Compile_packages_and_dependencies)
- [GoReleaser](https://goreleaser.com/) (studied for release conventions; not
  used because it cannot own this project's Web, 7-Zip, FRP, and updater asset
  contract)
