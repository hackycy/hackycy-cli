import type { Command } from 'commander'

export function register(parent: Command): void {
  const cmd = parent
    .command('config')
    .description('Manage git fork provider instances')

  cmd
    .command('add')
    .description('Add a provider instance')
    .action(async () => {
      const { runForkConfigAdd } = await import('./config')
      await runForkConfigAdd()
    })

  cmd
    .command('remove')
    .description('Remove a provider instance')
    .action(async () => {
      const { runForkConfigRemove } = await import('./config')
      await runForkConfigRemove()
    })

  cmd
    .command('list')
    .description('List configured instances')
    .action(async () => {
      const { runForkConfigList } = await import('./config')
      await runForkConfigList()
    })
}
