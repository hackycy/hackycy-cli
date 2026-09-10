import type { ForkInstanceConfig } from './types'
import { decrypt, deriveKey, encrypt } from './crypto'
import { readConfig, updateConfig } from './store'

export async function addInstance(
  name: string,
  host: string,
  type: 'github' | 'gitlab',
  token: string,
  scheme: 'http' | 'https' = 'https',
): Promise<void> {
  await updateConfig(async (config) => {
    const key = await deriveKey(config.salt)
    const encryptedToken = encrypt(token, key)
    config.fork.instances[name] = { host, scheme, type, token: encryptedToken }
  })
}

export async function removeInstance(name: string): Promise<boolean> {
  return updateConfig((config) => {
    if (!(name in config.fork.instances))
      return false
    delete config.fork.instances[name]
    return true
  })
}

export async function getInstanceByName(name: string): Promise<(ForkInstanceConfig & { decryptedToken: string }) | null> {
  const config = await readConfig()
  const instance = config.fork.instances[name]
  if (!instance)
    return null
  const key = await deriveKey(config.salt)
  const decryptedToken = decrypt(instance.token, key)
  return { ...instance, decryptedToken }
}

export async function getInstanceByHost(host: string): Promise<{ name: string, instance: ForkInstanceConfig, decryptedToken: string } | null> {
  const config = await readConfig()
  for (const [name, instance] of Object.entries(config.fork.instances)) {
    if (instance.host === host) {
      const key = await deriveKey(config.salt)
      const decryptedToken = decrypt(instance.token, key)
      return { name, instance, decryptedToken }
    }
  }
  return null
}

export async function listInstances(): Promise<Record<string, ForkInstanceConfig>> {
  const config = await readConfig()
  return config.fork.instances
}
