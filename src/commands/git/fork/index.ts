import type { Command } from 'commander'

export function register(parent: Command): void {
  parent
    .command('fork <repo> [dest]')
    .description('Download a repo without git history (supports GitHub/GitLab, public/private)')
    .action(async (repo: string, dest?: string) => {
      const { runGitFork } = await import('./fork')
      await runGitFork(repo, dest)
    })
}
