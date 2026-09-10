import type { Command } from 'commander'

export function register(program: Command): void {
  program
    .command('upgrade')
    .description('Upgrade cli to the latest version')
    .action(async () => {
      const { upgradeCli } = await import('./upgrade')
      await upgradeCli()
    })
}
