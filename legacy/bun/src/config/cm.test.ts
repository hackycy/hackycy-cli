import process from 'node:process'
import { afterEach, describe, expect, test } from 'bun:test'
import { resolveCmProfile } from './cm'

const originalEnvironment = { ...process.env }

afterEach(() => {
  for (const key of Object.keys(process.env)) {
    if (!(key in originalEnvironment))
      delete process.env[key]
  }
  Object.assign(process.env, originalEnvironment)
})

function setProviderEnvironment(): void {
  process.env.HOME = `/tmp/ycy-cm-config-test-${process.pid}`
  process.env.YCY_CM_BASE_URL = 'https://provider.test'
  process.env.YCY_CM_MODEL = 'test-model'
  process.env.YCY_CM_API_KEY = 'test-key'
  delete process.env.YCY_CM_TIMEOUT_MS
}

describe('resolveCmProfile', () => {
  test('uses a five-minute default timeout', async () => {
    setProviderEnvironment()

    await expect(resolveCmProfile()).resolves.toMatchObject({ timeoutMs: 300_000 })
  })

  test('preserves environment timeout and lets a command override win', async () => {
    setProviderEnvironment()
    process.env.YCY_CM_TIMEOUT_MS = '12345'

    await expect(resolveCmProfile()).resolves.toMatchObject({ timeoutMs: 12345 })
    await expect(resolveCmProfile(undefined, 123456)).resolves.toMatchObject({ timeoutMs: 123456 })
  })
})
