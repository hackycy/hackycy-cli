import type { Dirent } from 'node:fs'
import type { CleanCandidate } from './types'
import fs from 'node:fs/promises'
import path from 'node:path'
import { CLEAN_RULES } from './rules'

// Directories that should never be recursed into
const SKIP_DIRS = new Set(['.git', '.svn', '.hg', '__pycache__'])

export async function scanForCandidates(
  dir: string,
  maxDepth: number,
  currentDepth = 0,
): Promise<CleanCandidate[]> {
  if (currentDepth > maxDepth)
    return []

  let entries: Dirent[]
  try {
    entries = await fs.readdir(dir, { withFileTypes: true })
  }
  catch {
    return []
  }

  const candidates: CleanCandidate[] = []

  await Promise.all(entries.map(async (entry) => {
    if (!entry.isDirectory())
      return
    if (entry.name.startsWith('.') || SKIP_DIRS.has(entry.name))
      return

    const fullPath = path.join(dir, entry.name)

    // Check each rule; stop at first match
    for (const rule of CLEAN_RULES) {
      const isMatch = await rule.match(entry.name, dir)
      if (isMatch) {
        candidates.push({ rule, path: fullPath })
        return // Don't recurse into matched dirs
      }
    }

    // No rule matched — recurse deeper
    const nested = await scanForCandidates(fullPath, maxDepth, currentDepth + 1)
    candidates.push(...nested)
  }))

  return candidates
}
