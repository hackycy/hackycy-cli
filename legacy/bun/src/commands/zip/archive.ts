import fs from 'node:fs/promises'
import path from 'node:path'
import { zipSync as fflateZipSync } from 'fflate'

const DEFAULT_GLOB_PATTERN = '**/*'

const ZIP_GLOB_OPTIONS = [
  { value: DEFAULT_GLOB_PATTERN, label: 'All files (recommended)' },
  { value: '**/*.html', label: 'HTML files' },
  { value: '**/*.js', label: 'JavaScript files' },
  { value: '**/*.css', label: 'CSS files' },
  { value: 'assets/**/*', label: 'assets directory' },
  { value: 'static/**/*', label: 'static directory' },
] as const

type FflateFiles = Parameters<typeof fflateZipSync>[0]

interface ArchiveEntry {
  relative: string
  absolute: string
}

function isUint8ArrayLike(value: unknown): value is Uint8Array {
  return value instanceof Uint8Array
}

export async function collectArchiveFiles(
  dir: string,
  patterns: string | string[] = DEFAULT_GLOB_PATTERN,
): Promise<ArchiveEntry[]> {
  const patternList = Array.isArray(patterns) ? patterns : [patterns]
  const seen = new Set<string>()
  const collected: ArchiveEntry[] = []

  for (const pattern of patternList) {
    const glob = new Bun.Glob(pattern)
    for await (const file of glob.scan({ cwd: dir, onlyFiles: true })) {
      const absolute = path.join(dir, file)
      let stat
      try {
        stat = await fs.lstat(absolute)
      }
      catch {
        continue
      }

      if (!stat.isFile())
        continue

      if (!seen.has(absolute)) {
        seen.add(absolute)
        collected.push({ relative: file, absolute })
      }
    }
  }

  return collected
}

export async function buildZipData(fileEntries: ArchiveEntry[], outputPath: string, withDir?: string): Promise<{ zipData: Uint8Array, includedCount: number }> {
  const fflateFiles: FflateFiles = Object.create(null) as FflateFiles
  let includedCount = 0

  for (const entry of fileEntries) {
    if (entry.absolute === outputPath)
      continue

    const relKey = entry.relative.split(path.sep).join('/')
    const zipKey = withDir ? `${withDir}/${relKey}` : relKey

    const fileBuffer = await Bun.file(entry.absolute).arrayBuffer()
    const fileBytes = new Uint8Array(fileBuffer)

    if (!isUint8ArrayLike(fileBytes))
      throw new Error(`Invalid file bytes for: ${entry.relative}`)

    fflateFiles[zipKey] = fileBytes
    includedCount += 1
  }

  if (includedCount === 0)
    throw new Error('No valid files matched after filtering.')

  return {
    zipData: fflateZipSync(fflateFiles, { level: 6 }),
    includedCount,
  }
}

export async function writeZipFile(outputPath: string, zipData: Uint8Array): Promise<void> {
  await Bun.write(outputPath, zipData)
}

export { DEFAULT_GLOB_PATTERN, ZIP_GLOB_OPTIONS }
export type { ArchiveEntry }
