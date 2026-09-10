import type { CleanAction } from './types'
import fs from 'node:fs/promises'
import path from 'node:path'
import { findDirsByName } from './scanner'

async function exists(p: string): Promise<boolean> {
  try {
    await fs.access(p)
    return true
  }
  catch {
    return false
  }
}

const LOCKFILE_NAMES = [
  'package-lock.json',
  'yarn.lock',
  'pnpm-lock.yaml',
  'bun.lock',
  'bun.lockb',
]

const AI_AGENT_DIRS = [
  '.claude',
  '.agents',
  '.cursor',
  '.copilot',
  '.windsurf',
  '.aider',
]

export const CLEAN_ACTIONS: CleanAction[] = [
  {
    id: 'node-dist',
    label: 'Node project — delete ./dist',
    scan: async (cwd) => {
      const p = path.join(cwd, 'dist')
      return (await exists(p)) ? [p] : []
    },
  },
  {
    id: 'node-node_modules',
    label: 'Node project — delete ./node_modules',
    scan: async (cwd) => {
      const p = path.join(cwd, 'node_modules')
      return (await exists(p)) ? [p] : []
    },
  },
  {
    id: 'monorepo-dist',
    label: 'Monorepo — delete all dist dirs (recursive)',
    scan: (cwd, depth) => findDirsByName(cwd, 'dist', depth),
  },
  {
    id: 'monorepo-node_modules',
    label: 'Monorepo — delete all node_modules dirs (recursive)',
    scan: (cwd, depth) => findDirsByName(cwd, 'node_modules', depth),
  },
  {
    id: 'node-lockfile',
    label: 'Node project — delete lockfile(s)',
    scan: async (cwd) => {
      const found: string[] = []
      for (const name of LOCKFILE_NAMES) {
        const p = path.join(cwd, name)
        if (await exists(p))
          found.push(p)
      }
      return found
    },
  },
  {
    id: 'ai-agent',
    label: 'AI agent config dirs (.claude, .cursor, .copilot…)',
    scan: async (cwd) => {
      const found: string[] = []
      for (const name of AI_AGENT_DIRS) {
        const p = path.join(cwd, name)
        if (await exists(p))
          found.push(p)
      }
      return found
    },
  },
]
