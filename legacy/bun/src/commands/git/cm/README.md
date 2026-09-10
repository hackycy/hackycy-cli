# `git cm`

`ycy git cm` generates one Angular-style commit message from the authoritative Git scope. It can print a message, stage a selected set of files, create a confirmed commit, and push that commit.

## Commands

| Command | Behavior |
| --- | --- |
| `ycy git cm` | Generate and print a message for every uncommitted change. |
| `ycy git cm --dry-run` | Generate and print only; Git state remains unchanged. |
| `ycy git cm --stage` | Select uncommitted files, stage them, generate a message, and confirm a commit. |
| `ycy git cm --stage-all` | Run `git add -A`, generate from the index, and confirm a commit. |
| `ycy git cm --staged` | Generate from the complete index and confirm a commit. |
| `ycy git cm --push [remote]` | Push after the applicable commit flow; defaults to `origin`. |
| `ycy git cm --stage-push [remote]` | Select, stage, commit, then push; defaults to `origin`. |
| `ycy git cm --body` | Allow a short validated body below the subject. |
| `ycy git cm --timeout-ms <milliseconds>` | Set the provider request timeout for this invocation. |

Use `--profile <name>` to choose a configured provider profile and `--lang en|zh` to select the output language.
Provider request timeouts use milliseconds and default to `300000` (five minutes). A command-line override takes precedence over `YCY_CM_TIMEOUT_MS`, the selected profile's `timeoutMs`, and the default.

## Generation Guarantees

The command makes one model request per generation. It compiles structured semantic evidence from the complete Git scope instead of truncating a raw diff. Evidence includes an independent tree of changed, inspectable directories so the model can infer the affected module from nested paths rather than a file stem. The tree is retained when evidence is compacted; low-priority facts are reduced first. Evidence also prioritizes scope totals, renames, dependency and configuration changes, public declarations, tests, hunk context, and representative behavior across every change cluster. Facts are included whole or omitted whole.

Evidence budgeting never changes the files that can be selected, staged, committed, or pushed. `--stage` always lists the full uncommitted file set, including repositories with more than 100 changed files.

Each generation captures an immutable Git snapshot. Before committing, the command recaptures the same scope and refuses to commit when it has changed. Model output must be a valid Angular message; invalid output is rejected without retrying or committing.

The local evidence compiler targets about 3,000 serialized prompt tokens with a 4,000-token ceiling. It groups facts by directory and file so repeated paths are sent once. This local estimate controls evidence selection only. Commit-message output uses a separate hard-coded 4,096-token request budget; neither budget adds a configuration option. Provider usage and the local evidence estimate are shown separately with the generated message when available.

## Content Protection

Sensitive paths such as `.env`, private keys, and certificates, plus binary files, lockfiles, generated output, and large files, remain in the Git scope but are metadata-only or redacted in model evidence. They do not add directory entries. Their contents can contribute to the local snapshot hash but are never sent to the provider. If an extractor cannot inspect one text file, the command falls back to its path, status, hunk context, and change statistics.

## Evaluation

Prepare a local corpus of 30 to 100 historical commits without contacting a provider:

```bash
bun scripts/evaluate-git-cm.ts --prepare --count=30 --output=.tmp/git-cm-evaluation.json
```

With a configured CM profile, omit `--prepare` to compare the legacy raw-diff input with semantic evidence. The artifact records messages, changed-file counts, provider usage, request counts, local estimates, and evidence. Historical subjects are only seed labels; inspect parent diffs before making accuracy claims.

Create blinded candidate material for manual comparison:

```bash
bun scripts/evaluate-git-cm.ts --blind-review=.tmp/git-cm-evaluation.json --output=.tmp/git-cm-blind-review.json
```

The blind artifact hides the legacy/semantic assignment and original subjects. Review each parent diff before decoding its mapping from the source artifact.

## Checks

```bash
bun test src/commands/git/cm
bun run typecheck
bun run lint
```
