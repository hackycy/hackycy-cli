import type { Buffer } from 'node:buffer'
import type { AppConfig, StoredTunnelConnection } from './types'
import { createHmac } from 'node:crypto'
import process from 'node:process'
import { decrypt, deriveKey, encrypt } from './crypto'
import { ensureConfig, updateConfig } from './store'

const INSTANCE_ID_PREFIX = 'v1_'
const INSTANCE_ID_PATTERN = /^v1_[\w-]{43}$/
const INSTANCE_ID_DOMAIN = 'ycy:tunnel-client-instance:v1\0'
const MAX_REMEMBERED_CONNECTIONS = 32

export interface RememberedTunnelConnection {
  id: string
  server: string
  token: string
  lastAuthenticatedAt: string
}

export interface TunnelConnectionCatalog {
  connections: RememberedTunnelConnection[]
  instanceId: (server: URL, token: string) => string
}

interface ValidStoredConnection extends RememberedTunnelConnection {
  stored: StoredTunnelConnection
}

function instanceId(server: URL, token: string, key: Buffer): string {
  const digest = createHmac('sha256', key)
    .update(INSTANCE_ID_DOMAIN)
    .update(server.origin)
    .update('\0')
    .update(token)
    .digest('base64url')
  return `${INSTANCE_ID_PREFIX}${digest}`
}

function normalizedStoredServer(value: string): URL | undefined {
  try {
    const server = new URL(value)
    if (!['http:', 'https:'].includes(server.protocol)
      || server.username
      || server.password
      || server.search
      || server.hash
      || (server.pathname !== '/' && server.pathname !== '')
      || server.origin !== value) {
      return undefined
    }
    return server
  }
  catch {
    return undefined
  }
}

function validConnections(config: AppConfig, key: Buffer): ValidStoredConnection[] {
  const valid: ValidStoredConnection[] = []
  for (const [id, stored] of Object.entries(config.tunnel?.connections ?? {})) {
    if (!INSTANCE_ID_PATTERN.test(id)
      || !stored
      || typeof stored.server !== 'string'
      || typeof stored.token !== 'string'
      || typeof stored.lastAuthenticatedAt !== 'string'
      || !Number.isFinite(Date.parse(stored.lastAuthenticatedAt))) {
      continue
    }
    const server = normalizedStoredServer(stored.server)
    if (!server)
      continue
    try {
      const token = decrypt(stored.token, key).trim()
      if (!token || instanceId(server, token, key) !== id)
        continue
      valid.push({ id, server: server.origin, token, lastAuthenticatedAt: stored.lastAuthenticatedAt, stored })
    }
    catch {
      // One corrupt entry must not make the remaining catalog unusable.
    }
  }
  return valid.sort((left, right) => right.lastAuthenticatedAt.localeCompare(left.lastAuthenticatedAt) || left.id.localeCompare(right.id))
}

export async function readTunnelConnectionCatalog(env: NodeJS.ProcessEnv = process.env): Promise<TunnelConnectionCatalog> {
  const config = await ensureConfig(env)
  const key = await deriveKey(config.salt)
  return {
    connections: validConnections(config, key).map(({ stored: _stored, ...connection }) => connection),
    instanceId: (server, token) => instanceId(server, token, key),
  }
}

export async function rememberTunnelConnection(
  connection: { server: URL, token: string },
  env: NodeJS.ProcessEnv = process.env,
  authenticatedAt: Date = new Date(),
): Promise<void> {
  await updateConfig(async (config) => {
    const key = await deriveKey(config.salt)
    const id = instanceId(connection.server, connection.token, key)
    const entries = validConnections(config, key)
      .filter(entry => entry.id !== id)
      .map(entry => [entry.id, entry.stored] as const)
    entries.push([id, {
      server: connection.server.origin,
      token: encrypt(connection.token, key),
      lastAuthenticatedAt: authenticatedAt.toISOString(),
    }])
    entries.sort((left, right) => right[1].lastAuthenticatedAt.localeCompare(left[1].lastAuthenticatedAt) || left[0].localeCompare(right[0]))
    config.tunnel = { connections: Object.fromEntries(entries.slice(0, MAX_REMEMBERED_CONNECTIONS)) }
  }, env)
}
