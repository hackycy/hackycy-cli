import type { FileDiffMetadata } from '@pierre/diffs'
import { parseDiffFromFile } from '@pierre/diffs'

interface TextDiffInput {
  snapshotId: string
  entryId: number
  path: string
  baseline: string
  target: string
  ignoreLineEndings?: boolean
  ignoreWhitespace: boolean
}

export function parseTextDiff({ snapshotId, entryId, path, baseline, target, ignoreWhitespace, ignoreLineEndings = false }: TextDiffInput): FileDiffMetadata {
  const diff = parseDiffFromFile(
    { name: path, contents: baseline },
    { name: path, contents: target },
    { ignoreWhitespace, stripTrailingCr: ignoreLineEndings },
  )
  // A parsed diff owns its cache identity: the same source files can produce
  // incompatible hunk layouts under different comparison options.
  diff.cacheKey = `${snapshotId}:${entryId}:text-diff:${ignoreWhitespace ? 'ignore-whitespace' : 'exact'}:${ignoreLineEndings ? 'ignore-line-endings' : 'exact-line-endings'}`
  return diff
}
