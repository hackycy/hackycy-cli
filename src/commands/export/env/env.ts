import type { EnvOptions } from './types'
import path from 'node:path'
import process from 'node:process'
import * as p from '@clack/prompts'
import { parse } from 'dotenv'

const EXCLUDED_SUFFIXES = new Set(['example', 'sample'])

function parseEnvFile(content: string): Record<string, string> {
  return parse(content) as Record<string, string>
}

function getEnvSuffix(filename: string): string | null {
  // filename must start with '.env'
  const rest = filename.slice(4)
  if (!rest)
    return null // just '.env'
  if (!rest.startsWith('.'))
    return null // e.g. '.envrc' – skip
  const suffix = rest.slice(1) // e.g. 'local', 'prod', 'test.local'
  if (EXCLUDED_SUFFIXES.has(suffix))
    return null
  return suffix
}

export async function exportEnv(options: EnvOptions): Promise<void> {
  const dir = path.resolve(options.dir ?? process.cwd())

  // Discover .env files
  const glob = new Bun.Glob('.env*')
  const allFiles = [...glob.scanSync({ cwd: dir, onlyFiles: true, dot: true })]
    .map(f => path.basename(f))
    .filter(name => name.startsWith('.env'))
    .sort()

  const baseFile = allFiles.includes('.env') ? '.env' : null
  const envFiles = allFiles
    .filter(name => name !== '.env')
    .filter(name => getEnvSuffix(name) !== null)

  if (!baseFile && envFiles.length === 0) {
    throw new Error(`No .env files found in ${dir}`)
  }

  let selectedFile: string | null = null

  if (envFiles.length > 0) {
    if (options.env) {
      const target = `.env.${options.env}`
      if (!envFiles.includes(target)) {
        throw new Error(`No .env.${options.env} file found in ${dir}`)
      }
      selectedFile = target
    }
    else {
      const choices = envFiles.map((name) => {
        const suffix = getEnvSuffix(name)!
        return { value: name, label: suffix }
      })

      const selected = await p.select({
        message: 'Select environment',
        options: choices,
      })

      if (p.isCancel(selected)) {
        p.cancel('Cancelled')
        process.exit(0)
      }

      selectedFile = selected as string
    }
  }

  // Parse and merge
  let result: Record<string, string> = {}

  if (baseFile) {
    const content = await Bun.file(path.join(dir, baseFile)).text()
    result = { ...parseEnvFile(content) }
  }

  if (selectedFile) {
    const content = await Bun.file(path.join(dir, selectedFile)).text()
    result = { ...result, ...parseEnvFile(content) }
  }

  const json = JSON.stringify(result, null, 2)

  if (options.out) {
    p.outro(`Writing output to ${options.out}`)
    await Bun.write(path.resolve(options.out), json)
  }
  else {
    p.outro('Exported variables:')
    console.log(json)
  }
}
