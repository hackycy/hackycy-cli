import type { Command } from 'commander'

export function register(program: Command): void {
  const config = program
    .command('config')
    .description('Manage ycy configuration')

  const fork = config
    .command('fork')
    .description('Manage git fork provider instances')

  fork
    .command('add')
    .description('Add a provider instance')
    .action(async () => {
      const { runForkConfigAdd } = await import('./fork')
      await runForkConfigAdd()
    })

  fork
    .command('remove')
    .description('Remove a provider instance')
    .action(async () => {
      const { runForkConfigRemove } = await import('./fork')
      await runForkConfigRemove()
    })

  fork
    .command('list')
    .description('List configured provider instances')
    .action(async () => {
      const { runForkConfigList } = await import('./fork')
      await runForkConfigList()
    })

  const cm = config
    .command('cm')
    .description('Manage commit message provider profiles')

  cm
    .command('add')
    .description('Add an OpenAI-compatible provider profile')
    .action(async () => {
      const { runCmConfigAdd } = await import('./cm')
      await runCmConfigAdd()
    })

  cm
    .command('list')
    .description('List commit message provider profiles')
    .action(async () => {
      const { runCmConfigList } = await import('./cm')
      await runCmConfigList()
    })

  cm
    .command('use <profile>')
    .description('Set the default commit message provider profile')
    .action(async (profile: string) => {
      const { runCmConfigUse } = await import('./cm')
      await runCmConfigUse(profile)
    })

  cm
    .command('remove <profile>')
    .description('Remove a commit message provider profile')
    .action(async (profile: string) => {
      const { runCmConfigRemove } = await import('./cm')
      await runCmConfigRemove(profile)
    })

  cm
    .command('set <profile> <key> <value>')
    .description('Set an optional commit message provider profile value')
    .action(async (profile: string, key: string, value: string) => {
      const { runCmConfigSet } = await import('./cm')
      await runCmConfigSet(profile, key, value)
    })

  cm
    .command('test [profile]')
    .description('Test a commit message provider profile')
    .action(async (profile?: string) => {
      const { runCmConfigTest } = await import('./cm')
      await runCmConfigTest(profile)
    })
}
