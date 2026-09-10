import type { Dirent } from 'node:fs'
import fs from 'node:fs/promises'
import path from 'node:path'

const SKIP_DIRS = new Set(['.git', '.svn', '.hg', '__pycache__'])

export async function findDirsByName(
  dir: string,
  targetName: string,
  maxDepth: number,
  currentDepth = 0,
): Promise<string[]> {
  if (currentDepth > maxDepth)
    return []

  let entries: Dirent[]
  try {
    entries = await fs.readdir(dir, { withFileTypes: true })
  }
  catch {
    return []
  }

  const results: string[] = []

  await Promise.all(entries.map(async (entry) => {
    if (!entry.isDirectory())
      return

    const fullPath = path.join(dir, entry.name)

    if (entry.name === targetName) {
      results.push(fullPath)
      return // don't recurse into matched dir
    }

    if (SKIP_DIRS.has(entry.name) || entry.name.startsWith('.'))
      return

    const nested = await findDirsByName(fullPath, targetName, maxDepth, currentDepth + 1)
    results.push(...nested)
  }))

  return results
}
