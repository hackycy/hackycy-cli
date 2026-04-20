import type { CleanRule } from './types'
import fs from 'node:fs/promises'
import path from 'node:path'

async function hasPackageJson(dir: string): Promise<boolean> {
  try {
    await fs.access(path.join(dir, 'package.json'))
    return true
  }
  catch {
    return false
  }
}

export const CLEAN_RULES: CleanRule[] = [
  {
    id: 'node_modules',
    label: 'node_modules',
    category: 'Node.js',
    match: name => name === 'node_modules',
  },
  {
    id: 'dist',
    label: 'dist',
    category: 'Node.js',
    match: async (name, parentDir) => {
      if (name !== 'dist')
        return false
      return hasPackageJson(parentDir)
    },
  },
]
