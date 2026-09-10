import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'

const select = mock(() => '.env')

mock.module('@clack/prompts', () => ({
  cancel: mock(),
  isCancel: () => false,
  outro: mock(),
  select,
}))

const { exportEnv } = await import('./env')

let dir: string

beforeEach(async () => {
  dir = await mkdtemp(path.join(tmpdir(), 'ycy-export-env-'))
  select.mockReset()
})

afterEach(async () => {
  await rm(dir, { recursive: true, force: true })
})

async function writeEnvFiles(): Promise<void> {
  await writeFile(path.join(dir, '.env'), 'BASE_ONLY=base\nSHARED=base\n')
  await writeFile(path.join(dir, '.env.development'), 'ENV_ONLY=development\nSHARED=development\n')
}

describe('exportEnv', () => {
  test('exports only the requested environment file by default', async () => {
    await writeEnvFiles()
    const out = path.join(dir, 'output.json')

    await exportEnv({ dir, env: 'development', out })

    await expect(readFile(out, 'utf8')).resolves.toBe(JSON.stringify({
      ENV_ONLY: 'development',
      SHARED: 'development',
    }, null, 2))
  })

  test('merges .env before the requested environment file', async () => {
    await writeEnvFiles()
    const out = path.join(dir, 'output.json')

    await exportEnv({ dir, env: 'development', merge: true, out })

    await expect(readFile(out, 'utf8')).resolves.toBe(JSON.stringify({
      BASE_ONLY: 'base',
      SHARED: 'development',
      ENV_ONLY: 'development',
    }, null, 2))
  })

  test('allows selecting .env without merging it', async () => {
    await writeEnvFiles()
    const out = path.join(dir, 'output.json')
    select.mockReturnValue('.env')

    await exportEnv({ dir, out })

    expect(select).toHaveBeenCalledWith({
      message: 'Select environment',
      options: [
        { value: '.env', label: 'default' },
        { value: '.env.development', label: 'development' },
      ],
    })
    await expect(readFile(out, 'utf8')).resolves.toBe(JSON.stringify({
      BASE_ONLY: 'base',
      SHARED: 'base',
    }, null, 2))
  })

  test('hides .env from selection when merging', async () => {
    await writeEnvFiles()
    const out = path.join(dir, 'output.json')
    select.mockReturnValue('.env.development')

    await exportEnv({ dir, merge: true, out })

    expect(select).toHaveBeenCalledWith({
      message: 'Select environment',
      options: [{ value: '.env.development', label: 'development' }],
    })
  })
})
