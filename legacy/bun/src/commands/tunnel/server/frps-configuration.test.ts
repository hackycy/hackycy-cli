import type { ServerTunnelConfig } from '../types'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { tomlCodec } from '../frp/toml'
import { TunnelError } from '../types'
import { FrpsConfiguration, FrpsConfigurationPaths, MAX_CUSTOM_404_PAGE_BYTES } from './frps-configuration'

const temporaryDirectories: string[] = []

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map(directory => rm(directory, { recursive: true, force: true })))
})

function serverConfig(dataDir: string): ServerTunnelConfig {
  return {
    address: '127.0.0.1',
    controlPort: 7500,
    frpPort: 7000,
    httpPort: 8080,
    portRange: { start: 20000, end: 20100 },
    dataDir,
    adminUser: 'admin',
    adminPassword: 'environment-secret',
  }
}

async function temporaryDirectory(): Promise<string> {
  const directory = await mkdtemp(path.join(tmpdir(), 'ycy-frps-configuration-'))
  temporaryDirectories.push(directory)
  return directory
}

describe('FrpsConfiguration', () => {
  test('keeps generated TOML and the custom page together in the server data directory', async () => {
    const dataDir = await temporaryDirectory()
    const paths = new FrpsConfigurationPaths(dataDir)
    const configuration = new FrpsConfiguration(serverConfig(dataDir))

    expect(paths.tomlPath).toBe(path.join(dataDir, 'frps.toml'))
    expect(paths.custom404PagePath).toBe(path.join(dataDir, '404.html'))
    expect(await configuration.getCustom404Page()).toBe('')
    expect(tomlCodec.parse(configuration.render('internal-token'))).toMatchObject({ custom404Page: paths.custom404PagePath })

    await configuration.setCustom404Page('<main>first version</main>')
    expect(await readFile(paths.custom404PagePath, 'utf8')).toBe('<main>first version</main>')
    expect(await configuration.getCustom404Page()).toBe('<main>first version</main>')

    await configuration.setCustom404Page('')
    expect(await Bun.file(paths.custom404PagePath).exists()).toBe(false)
    expect(await configuration.getCustom404Page()).toBe('')
  })

  test('rejects oversized content and operational file failures', async () => {
    const dataDir = await temporaryDirectory()
    const configuration = new FrpsConfiguration(serverConfig(dataDir))

    await expect(configuration.setCustom404Page('x'.repeat(MAX_CUSTOM_404_PAGE_BYTES + 1))).rejects.toEqual(
      new TunnelError('INVALID_CUSTOM_404_PAGE', 'Custom 404 page must not exceed 512 KiB'),
    )

    const blocked = path.join(dataDir, 'blocked')
    await writeFile(blocked, 'not a directory')
    const broken = new FrpsConfiguration(serverConfig(blocked))
    await expect(broken.getCustom404Page()).rejects.toThrow('Could not read custom 404 page')
    await expect(broken.setCustom404Page('<main>blocked</main>')).rejects.toThrow('Could not write custom 404 page')
  })
})
