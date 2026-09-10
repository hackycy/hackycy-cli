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

  if (options.env) {
    const target = `.env.${options.env}`
    if (!envFiles.includes(target)) {
      throw new Error(`No .env.${options.env} file found in ${dir}`)
    }
    selectedFile = target
  }
  else {
    const selectableFiles = options.merge
      ? envFiles
      : baseFile
        ? [baseFile, ...envFiles]
        : envFiles

    if (selectableFiles.length === 1 && selectableFiles[0] === baseFile) {
      selectedFile = selectableFiles[0]!
    }
    else if (selectableFiles.length > 0) {
      const choices = selectableFiles.map((name) => {
        const suffix = getEnvSuffix(name)
        return { value: name, label: suffix ?? 'default' }
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

  // Merge .env first only when explicitly requested.
  let result: Record<string, string> = {}

  const filesToExport = [
    ...(options.merge && baseFile ? [baseFile] : []),
    ...(selectedFile ? [selectedFile] : []),
  ]

  for (const file of filesToExport) {
    const content = await Bun.file(path.join(dir, file)).text()
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
