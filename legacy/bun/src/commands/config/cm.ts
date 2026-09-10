import process from 'node:process'
import * as p from '@clack/prompts'
import ansis from 'ansis'
import { testCmProfile } from '../../config/client'
import {
  addCmProfile,
  listCmProfiles,
  removeCmProfile,
  resolveCmProfile,
  setCmProfileValue,
  setDefaultCmProfile,
} from '../../config/cm'
import { printTitle } from '../../shared/utils'

export async function runCmConfigAdd(): Promise<void> {
  printTitle()
  p.intro(ansis.cyan('CM Config — Add Profile'))

  const result = await p.group(
    {
      name: () =>
        p.text({
          message: 'Profile name',
          placeholder: 'e.g. openai, deepseek, work',
          validate: (v = '') => {
            if (!v.trim())
              return 'Name is required'
            if (/\s/.test(v))
              return 'Name cannot contain spaces'
          },
        }),
      baseURL: () =>
        p.text({
          message: 'OpenAI-compatible base URL',
          placeholder: 'https://api.openai.com/v1',
          validate: (v = '') => {
            if (!v.trim())
              return 'Base URL is required'
          },
        }),
      model: () =>
        p.text({
          message: 'Model',
          placeholder: 'gpt-4.1-mini',
          validate: (v = '') => {
            if (!v.trim())
              return 'Model is required'
          },
        }),
      apiKey: () =>
        p.password({
          message: 'API key',
          validate: (v = '') => {
            if (!v.trim())
              return 'API key is required'
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

  const spin = p.spinner()
  spin.start('Saving CM profile...')
  try {
    await addCmProfile(result.name, result.baseURL, result.model, result.apiKey)
    spin.stop('CM profile saved')
    p.outro(`Profile ${ansis.cyan(result.name)} added`)
  }
  catch (err) {
    spin.stop('Failed to save CM profile')
    p.log.error((err as Error).message)
    process.exit(1)
  }
}

export async function runCmConfigList(): Promise<void> {
  printTitle()
  console.log(ansis.dim('CM Config — Profiles'))
  console.log()

  const cm = await listCmProfiles()
  const entries = Object.entries(cm.profiles)
  if (entries.length === 0) {
    console.log(ansis.dim('No CM profiles configured. Run "ycy config cm add" to add one.'))
    return
  }

  for (const [name, profile] of entries) {
    const marker = cm.defaultProfile === name ? ansis.green('*') : ' '
    console.log(`${marker} ${ansis.cyan(name)} ${ansis.dim(profile.model)} ${ansis.dim(profile.baseURL)}`)
  }
  console.log()
}

export async function runCmConfigUse(name: string): Promise<void> {
  try {
    await setDefaultCmProfile(name)
    p.outro(`Default CM profile set to ${ansis.cyan(name)}`)
  }
  catch (err) {
    p.log.error((err as Error).message)
    process.exit(1)
  }
}

export async function runCmConfigRemove(name: string): Promise<void> {
  const confirmed = await p.confirm({
    message: `Remove CM profile "${name}"?`,
  })

  if (p.isCancel(confirmed) || !confirmed) {
    p.outro('Cancelled')
    return
  }

  const removed = await removeCmProfile(name)
  if (!removed) {
    p.log.error(`CM profile not found: ${name}`)
    process.exit(1)
  }

  p.outro(`Profile ${ansis.cyan(name)} removed`)
}

export async function runCmConfigSet(name: string, key: string, value: string): Promise<void> {
  try {
    await setCmProfileValue(name, key, value)
    p.outro(`Profile ${ansis.cyan(name)} updated`)
  }
  catch (err) {
    p.log.error((err as Error).message)
    process.exit(1)
  }
}

export async function runCmConfigTest(name?: string): Promise<void> {
  printTitle()
  p.intro(ansis.cyan('CM Config — Test Profile'))

  let profile
  try {
    profile = await resolveCmProfile(name)
  }
  catch (err) {
    p.log.error((err as Error).message)
    process.exit(1)
  }

  const spin = p.spinner()
  spin.start(`Testing ${profile.name}...`)
  try {
    const content = await testCmProfile(profile)
    spin.stop('CM profile is reachable')
    p.log.info(`Response: ${content}`)
    p.outro('Done')
  }
  catch (err) {
    spin.stop('CM profile test failed')
    p.log.error((err as Error).message)
    p.log.info(`Provider: ${profile.name}`)
    p.log.info(`Base URL: ${profile.baseURL}`)
    p.log.info(`Model: ${profile.model}`)
    process.exit(1)
  }
}
