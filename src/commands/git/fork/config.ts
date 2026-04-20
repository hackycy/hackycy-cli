import type { ForkConfig, InstanceConfig } from './types'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import { decrypt, deriveKey, encrypt, generateSalt, getConfigDir } from './crypto'

const CONFIG_FILE = 'config.json'

function getConfigPath(): string {
  return path.join(getConfigDir(), CONFIG_FILE)
}

export async function readConfig(): Promise<ForkConfig> {
  const file = Bun.file(getConfigPath())
  if (!(await file.exists())) {
    return { salt: generateSalt(), instances: {} }
  }
  const config: ForkConfig = await file.json()
  // Migrate legacy entries where scheme was embedded in host (e.g. "http://172.16.8.239:23081")
  let dirty = false
  for (const [, instance] of Object.entries(config.instances)) {
    if (instance.host.includes('://')) {
      const url = new URL(instance.host)
      instance.scheme = url.protocol.slice(0, -1) as 'http' | 'https'
      instance.host = url.host
      dirty = true
    }
  }
  if (dirty)
    await writeConfig(config)
  return config
}

export async function writeConfig(config: ForkConfig): Promise<void> {
  const dir = getConfigDir()
  await mkdir(dir, { recursive: true })
  await Bun.write(getConfigPath(), JSON.stringify(config, null, 2))
}

export async function addInstance(
  name: string,
  host: string,
  type: 'github' | 'gitlab',
  token: string,
  scheme: 'http' | 'https' = 'https',
): Promise<void> {
  const config = await readConfig()
  const key = await deriveKey(config.salt)
  const encryptedToken = encrypt(token, key)
  config.instances[name] = { host, scheme, type, token: encryptedToken }
  await writeConfig(config)
}

export async function removeInstance(name: string): Promise<boolean> {
  const config = await readConfig()
  if (!(name in config.instances))
    return false
  delete config.instances[name]
  await writeConfig(config)
  return true
}

export async function getInstanceByName(name: string): Promise<(InstanceConfig & { decryptedToken: string }) | null> {
  const config = await readConfig()
  const instance = config.instances[name]
  if (!instance)
    return null
  const key = await deriveKey(config.salt)
  const decryptedToken = decrypt(instance.token, key)
  return { ...instance, decryptedToken }
}

export async function getInstanceByHost(host: string): Promise<{ name: string, instance: InstanceConfig, decryptedToken: string } | null> {
  const config = await readConfig()
  for (const [name, instance] of Object.entries(config.instances)) {
    if (instance.host === host) {
      const key = await deriveKey(config.salt)
      const decryptedToken = decrypt(instance.token, key)
      return { name, instance, decryptedToken }
    }
  }
  return null
}

export async function listInstances(): Promise<Record<string, InstanceConfig>> {
  const config = await readConfig()
  return config.instances
}
