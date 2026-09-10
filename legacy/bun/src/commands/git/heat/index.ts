import type { Command } from 'commander'
import type { GitHeatOptions, HeatSort, HeatTarget } from './types'
import { parseIntArg } from '../../../shared/utils'

export function register(parent: Command): void {
  parent
    .command('heat')
    .description('Show frequently changed files and directories in recent commits')
    .option('-n, --limit <number>', 'Number of recent commits to inspect', parseIntArg)
    .option('-d, --days <number>', 'Number of recent days to inspect', parseIntArg)
    .option('-t, --type <type>', 'Report type: files or directories', parseHeatTarget, 'directories')
    .option('-s, --sort <sort>', 'Sort by count or path', parseHeatSort, 'path')
    .option('-r, --relative-time', 'Show Changed at as relative time')
    .option('-q, --query <text>', 'Highlight files or directories that contain text')
    .action(async (options: GitHeatOptions) => {
      const { runGitHeat } = await import('./heat')
      await runGitHeat(options)
    })
}

function parseHeatTarget(value: string): HeatTarget {
  if (value === 'files')
    return 'files'
  if (value === 'directories' || value === 'dirs')
    return 'directories'
  throw new Error(`'${value}' is not a valid report type. Use files or directories.`)
}

function parseHeatSort(value: string): HeatSort {
  if (value === 'count' || value === 'path')
    return value
  throw new Error(`'${value}' is not a valid sort. Use count or path.`)
}
