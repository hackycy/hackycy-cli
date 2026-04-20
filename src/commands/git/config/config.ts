import process from 'node:process'

import * as p from '@clack/prompts'
import ansis from 'ansis'
import { printTitle } from '../../../shared/utils'
import { addInstance, listInstances, removeInstance } from '../fork/config'

export async function runForkConfigAdd(): Promise<void> {
  printTitle()
  p.intro(ansis.cyan('Fork Config — Add Instance'))

  const result = await p.group(
    {
      name: () =>
        p.text({
          message: 'Instance name (alias)',
          placeholder: 'e.g. work, github, company-gl',
          validate: (v = '') => {
            if (!v.trim())
              return 'Name is required'
            if (/\s/.test(v))
              return 'Name cannot contain spaces'
          },
        }),
      host: () =>
        p.text({
          message: 'Host',
          placeholder: 'e.g. gitlab.company.com, github.com',
          validate: (v = '') => {
            if (!v.trim())
              return 'Host is required'
          },
        }),
      type: () =>
        p.select({
          message: 'Provider type',
          options: [
            { value: 'gitlab' as const, label: 'GitLab' },
            { value: 'github' as const, label: 'GitHub' },
          ],
        }),
      token: () =>
        p.password({
          message: 'Access token',
          validate: (v = '') => {
            if (!v.trim())
              return 'Token is required'
          },
        }),
    },
    {
      onCancel: () => {
        p.cancel('Cancelled')
        process.exit(0)
      },
    },
  )

  const s = p.spinner()
  s.start('Saving configuration...')

  try {
    await addInstance(result.name, result.host, result.type, result.token)
    s.stop('Configuration saved')
    p.outro(`Instance ${ansis.cyan(result.name)} (${result.host}) added successfully`)
  }
  catch (err) {
    s.stop('Failed to save')
    p.log.error((err as Error).message)
    process.exit(1)
  }
}

export async function runForkConfigRemove(): Promise<void> {
  printTitle()
  p.intro(ansis.cyan('Fork Config — Remove Instance'))

  const instances = await listInstances()
  const names = Object.keys(instances)

  if (names.length === 0) {
    p.log.info('No instances configured')
    p.outro('Nothing to remove')
    return
  }

  const name = await p.select({
    message: 'Select instance to remove',
    options: names.map(n => ({
      value: n,
      label: `${n} (${instances[n]!.host})`,
    })),
  })

  if (p.isCancel(name)) {
    p.cancel('Cancelled')
    process.exit(0)
  }

  const confirmed = await p.confirm({
    message: `Remove instance "${name}"?`,
  })

  if (p.isCancel(confirmed) || !confirmed) {
    p.outro('Cancelled')
    return
  }

  await removeInstance(name)
  p.outro(`Instance ${ansis.cyan(name)} removed`)
}

export async function runForkConfigList(): Promise<void> {
  printTitle()
  p.intro(ansis.cyan('Fork Config — Instances'))

  const instances = await listInstances()
  const entries = Object.entries(instances)

  if (entries.length === 0) {
    p.log.info('No instances configured. Run "ycy git fork-config add" to add one.')
    p.outro('')
    return
  }

  for (const [name, inst] of entries) {
    const tokenPreview = inst.token.length > 4
      ? `${inst.token.slice(0, 4)}***`
      : '***'
    p.log.info(
      `${ansis.bold(name)}\n`
      + `  Host:  ${inst.host}\n`
      + `  Type:  ${inst.type}\n`
      + `  Token: ${tokenPreview}`,
    )
  }

  p.outro(`${entries.length} instance(s) configured`)
}
