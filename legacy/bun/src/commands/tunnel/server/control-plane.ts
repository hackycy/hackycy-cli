import type { TunnelOptionsInput } from '../definition'
import type { ClientRecord, HttpTunnelDefinition, PortTunnelDefinition, TunnelDefinition, TunnelOptions, TunnelProtocol, TunnelSnapshot } from '../types'
import type { TunnelDatabase } from './database'
import { randomBytes, randomUUID } from 'node:crypto'
import { isIP } from 'node:net'
import { domainToASCII } from 'node:url'
import { normalizeTunnelLabel, normalizeTunnelOptions } from '../definition'
import { TunnelError } from '../types'

interface ClientRow {
  internal_id: string
  owner_account_id: string
  remark: string
  token: string
  desired_revision: number
  last_applied_revision: number
  revocation_pending: number
  created_at: string
  rotated_at: string | null
}

interface TunnelRow {
  id: string
  client_internal_id: string
  label: string
  protocol: TunnelProtocol
  custom_domains: string | null
  location: string | null
  server_port: number | null
  local_host: string
  local_port: number
  enabled: number
  options_json: string
  created_at: string
  updated_at: string
}

export type ControlPlaneEvent
  = | { type: 'desired_state', clientId: string, ownerAccountId: string }
    | { type: 'client_created', clientId: string, ownerAccountId: string }
    | { type: 'client_rotated', clientId: string, ownerAccountId: string }
    | { type: 'client_deleted', clientId: string, ownerAccountId: string }
    | { type: 'client_updated', clientId: string, ownerAccountId: string }

export interface TunnelMutationInput {
  protocol: TunnelProtocol
  customDomains?: string[]
  hostname?: string | null
  location?: string | null
  serverPort?: number | null
  localHost?: string
  localPort: number
  enabled?: boolean
  label?: string
  options?: TunnelOptionsInput
}

export interface TunnelPatchInput {
  protocol?: TunnelProtocol
  customDomains?: string[]
  hostname?: string | null
  location?: string | null
  serverPort?: number | null
  localHost?: string
  localPort?: number
  enabled?: boolean
  label?: string
  options?: TunnelOptionsInput
}

function clientRecord(row: ClientRow): ClientRecord {
  return {
    id: row.internal_id,
    ownerAccountId: row.owner_account_id,
    remark: row.remark,
    token: row.token,
    desiredRevision: row.desired_revision,
    lastAppliedRevision: row.last_applied_revision,
    revocationPending: row.revocation_pending === 1,
    createdAt: row.created_at,
    rotatedAt: row.rotated_at,
  }
}

function tunnelDefinition(row: TunnelRow): TunnelDefinition {
  const options = normalizeTunnelOptions(row.protocol, JSON.parse(row.options_json) as TunnelOptionsInput)
  const base = {
    id: row.id,
    label: row.label,
    localHost: row.local_host,
    localPort: row.local_port,
    enabled: row.enabled === 1,
    options,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  }
  if (row.protocol === 'http') {
    return {
      ...base,
      protocol: 'http',
      customDomains: JSON.parse(row.custom_domains!) as string[],
      location: row.location,
      serverPort: null,
    }
  }
  return { ...base, protocol: row.protocol, serverPort: row.server_port! }
}

type TunnelValues
  = | Omit<HttpTunnelDefinition, 'id' | 'createdAt' | 'updatedAt'>
    | Omit<PortTunnelDefinition, 'id' | 'createdAt' | 'updatedAt'>

export function normalizeExactHostname(input: string): string {
  const candidate = input.trim().replace(/\.$/, '')
  if (!candidate || candidate.length > 253 || candidate.includes('*') || candidate.includes('://') || /[\s/?#@:[\]]/.test(candidate) || isIP(candidate))
    throw new TunnelError('INVALID_HOSTNAME', 'HTTP Tunnel hostname must be one exact DNS hostname without scheme, path, port, IP address, or wildcard')
  const ascii = domainToASCII(candidate).toLowerCase()
  if (!ascii || ascii.length > 253) {
    throw new TunnelError('INVALID_HOSTNAME', 'HTTP Tunnel hostname is not a valid internationalized DNS hostname')
  }
  const labels = ascii.split('.')
  if (labels.length < 2 || labels.some(label => !label || label.length > 63 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label)))
    throw new TunnelError('INVALID_HOSTNAME', 'HTTP Tunnel hostname must contain valid DNS labels and a suffix')
  return ascii
}

export function normalizeCustomDomains(input: string[] | undefined, legacyHostname?: string | null): string[] {
  if (input !== undefined && legacyHostname != null)
    throw new TunnelError('INVALID_TUNNEL', 'Use customDomains instead of combining it with the legacy hostname field')
  const values = input ?? (legacyHostname ? [legacyHostname] : [])
  if (!values.length)
    throw new TunnelError('INVALID_HOSTNAME', 'HTTP Tunnel requires at least one custom domain')
  if (values.length > 32)
    throw new TunnelError('INVALID_HOSTNAME', 'HTTP Tunnel accepts at most 32 custom domains')
  return [...new Set(values.map(normalizeExactHostname))]
}

export function normalizeHttpLocation(input: string | null | undefined): string | null {
  if (input == null)
    return null
  const location = input.trim()
  const containsControlCharacter = [...location].some((character) => {
    const code = character.charCodeAt(0)
    return code < 0x20 || code === 0x7F
  })
  if (!location.startsWith('/') || location.length > 2048 || containsControlCharacter || /[\s\\?#]/.test(location))
    throw new TunnelError('INVALID_HTTP_ROUTE', 'HTTP Tunnel location must be a URL path beginning with / and must not contain spaces, query strings, or fragments')
  return location
}

function localEndpoint(localHost: string | undefined, localPort: number): { localHost: string, localPort: number } {
  const host = localHost?.trim() || '127.0.0.1'
  if (host.length > 253 || !/^[\w.:-]+$/.test(host))
    throw new TunnelError('INVALID_LOCAL_ENDPOINT', 'Local Endpoint host must be an IP address, hostname, or container service name')
  if (!Number.isSafeInteger(localPort) || localPort < 1 || localPort > 65535)
    throw new TunnelError('INVALID_LOCAL_ENDPOINT', 'Local Endpoint port must be an integer from 1 through 65535')
  return { localHost: host, localPort }
}

function protocol(value: string): TunnelProtocol {
  if (!['http', 'tcp', 'udp'].includes(value))
    throw new TunnelError('INVALID_TUNNEL', 'Tunnel protocol must be http, tcp, or udp')
  return value as TunnelProtocol
}

function token(): string {
  return `ycy_${randomBytes(32).toString('base64url')}`
}

function clientRemark(value: string): string {
  const remark = value.trim()
  if (remark.length > 100)
    throw new TunnelError('INVALID_CLIENT_REMARK', 'Client Remark must contain no more than 100 characters')
  return remark
}

function now(): string {
  return new Date().toISOString()
}

function constraintError(cause: unknown): never {
  const message = cause instanceof Error ? cause.message : String(cause)
  if (/UNIQUE constraint failed.*tunnel_http_routes/i.test(message))
    throw new TunnelError('RESOURCE_RESERVED', 'HTTP Tunnel custom domain and location are already reserved')
  if (/UNIQUE constraint failed.*(?:protocol|server_port)|tunnels_unique_transport_port/i.test(message))
    throw new TunnelError('RESOURCE_RESERVED', 'Port Tunnel protocol and server port are already reserved')
  throw cause
}

export class TunnelControlPlane {
  private readonly listeners = new Set<(event: ControlPlaneEvent) => void>()

  constructor(
    private readonly database: TunnelDatabase,
    private readonly portPool: { start: number, end: number },
  ) {}

  subscribe(listener: (event: ControlPlaneEvent) => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  private emit(event: ControlPlaneEvent): void {
    for (const listener of this.listeners)
      listener(event)
  }

  private incrementDesiredRevision(clientId: string): void {
    this.database.sqlite.query('UPDATE clients SET desired_revision = desired_revision + 1 WHERE internal_id = ?').run(clientId)
  }

  internalFrpToken(): string {
    const existing = this.database.sqlite.query<{ value: string }, []>('SELECT value FROM meta WHERE key = \'internal_frp_token\'').get()
    if (existing)
      return existing.value
    const value = randomBytes(32).toString('base64url')
    this.database.sqlite.query('INSERT INTO meta(key, value) VALUES(\'internal_frp_token\', ?)').run(value)
    return value
  }

  listClients(): ClientRecord[] {
    return this.database.sqlite.query<ClientRow, []>('SELECT * FROM clients ORDER BY created_at, internal_id').all().map(clientRecord)
  }

  listClientsForOwner(ownerAccountId: string): ClientRecord[] {
    return this.database.sqlite.query<ClientRow, [string]>('SELECT * FROM clients WHERE owner_account_id = ? ORDER BY created_at, internal_id').all(ownerAccountId).map(clientRecord)
  }

  getClient(id: string): ClientRecord {
    const row = this.database.sqlite.query<ClientRow, [string]>('SELECT * FROM clients WHERE internal_id = ?').get(id)
    if (!row)
      throw new TunnelError('NOT_FOUND', 'Trusted Tunnel Client was not found')
    return clientRecord(row)
  }

  getClientForOwner(id: string, ownerAccountId: string): ClientRecord {
    const row = this.database.sqlite.query<ClientRow, [string, string]>('SELECT * FROM clients WHERE internal_id = ? AND owner_account_id = ?').get(id, ownerAccountId)
    if (!row)
      throw new TunnelError('NOT_FOUND', 'Trusted Tunnel Client was not found')
    return clientRecord(row)
  }

  findClientByToken(value: string): ClientRecord | undefined {
    const row = this.database.sqlite.query<ClientRow, [string]>('SELECT * FROM clients WHERE token = ?').get(value)
    return row ? clientRecord(row) : undefined
  }

  createClient(ownerAccountId: string, remark = ''): ClientRecord {
    const id = randomUUID()
    const createdAt = now()
    this.database.sqlite.query('INSERT INTO clients(internal_id, owner_account_id, remark, token, created_at) VALUES(?, ?, ?, ?, ?)').run(id, ownerAccountId, clientRemark(remark), token(), createdAt)
    const created = this.getClient(id)
    this.emit({ type: 'client_created', clientId: id, ownerAccountId })
    return created
  }

  updateClientRemark(id: string, remark: string): ClientRecord {
    this.getClient(id)
    this.database.sqlite.query('UPDATE clients SET remark = ? WHERE internal_id = ?').run(clientRemark(remark), id)
    const updated = this.getClient(id)
    this.emit({ type: 'client_updated', clientId: id, ownerAccountId: updated.ownerAccountId })
    return updated
  }

  rotateClientToken(id: string): ClientRecord {
    const rotate = this.database.sqlite.transaction(() => {
      this.getClient(id)
      this.database.sqlite.query('UPDATE clients SET token = ?, revocation_pending = 1, rotated_at = ? WHERE internal_id = ?').run(token(), now(), id)
      return this.getClient(id)
    })
    const rotated = rotate.immediate()
    this.emit({ type: 'client_rotated', clientId: id, ownerAccountId: rotated.ownerAccountId })
    return rotated
  }

  acknowledgeReplacementToken(id: string): void {
    const result = this.database.sqlite.query('UPDATE clients SET revocation_pending = 0 WHERE internal_id = ? AND revocation_pending = 1').run(id)
    if (result.changes > 0)
      this.emit({ type: 'client_updated', clientId: id, ownerAccountId: this.getClient(id).ownerAccountId })
  }

  deleteClient(id: string): void {
    let ownerAccountId = ''
    const remove = this.database.sqlite.transaction(() => {
      ownerAccountId = this.getClient(id).ownerAccountId
      this.database.sqlite.query('DELETE FROM clients WHERE internal_id = ?').run(id)
    })
    remove.immediate()
    this.emit({ type: 'client_deleted', clientId: id, ownerAccountId })
  }

  listTunnels(clientId: string): TunnelDefinition[] {
    this.getClient(clientId)
    return this.database.sqlite.query<TunnelRow, [string]>('SELECT * FROM tunnels WHERE client_internal_id = ? ORDER BY created_at, id').all(clientId).map(tunnelDefinition)
  }

  getTunnel(id: string): TunnelDefinition & { clientId: string } {
    const row = this.database.sqlite.query<TunnelRow, [string]>('SELECT * FROM tunnels WHERE id = ?').get(id)
    if (!row)
      throw new TunnelError('NOT_FOUND', 'Tunnel Definition was not found')
    return { ...tunnelDefinition(row), clientId: row.client_internal_id }
  }

  getTunnelForOwner(id: string, ownerAccountId: string): TunnelDefinition & { clientId: string } {
    const row = this.database.sqlite.query<TunnelRow, [string, string]>(`
      SELECT tunnels.* FROM tunnels
      JOIN clients ON clients.internal_id = tunnels.client_internal_id
      WHERE tunnels.id = ? AND clients.owner_account_id = ?
    `).get(id, ownerAccountId)
    if (!row)
      throw new TunnelError('NOT_FOUND', 'Tunnel Definition was not found')
    return { ...tunnelDefinition(row), clientId: row.client_internal_id }
  }

  snapshot(clientId: string): TunnelSnapshot {
    const client = this.getClient(clientId)
    return { clientKey: client.id, revision: client.desiredRevision, tunnels: this.listTunnels(clientId) }
  }

  private availablePort(tunnelProtocol: 'tcp' | 'udp'): number {
    const rows = this.database.sqlite.query<{ server_port: number }, [string, number, number]>(
      'SELECT server_port FROM tunnels WHERE protocol = ? AND server_port BETWEEN ? AND ? ORDER BY server_port',
    ).all(tunnelProtocol, this.portPool.start, this.portPool.end)
    const reserved = new Set(rows.map(row => row.server_port))
    for (let candidate = this.portPool.start; candidate <= this.portPool.end; candidate++) {
      if (!reserved.has(candidate))
        return candidate
    }
    throw new TunnelError('PORT_POOL_EXHAUSTED', `No ${tunnelProtocol.toUpperCase()} server port is available in ${this.portPool.start}-${this.portPool.end}`)
  }

  private reserveHttpRoutes(tunnelId: string, customDomains: string[], location: string | null): void {
    for (const hostname of customDomains)
      this.database.sqlite.query('INSERT INTO tunnel_http_routes(tunnel_id, hostname, location) VALUES(?, ?, ?)').run(tunnelId, hostname, location ?? '')
  }

  private values(input: TunnelMutationInput, currentOptions?: TunnelOptions): TunnelValues {
    const tunnelProtocol = protocol(input.protocol)
    const endpoint = localEndpoint(input.localHost, input.localPort)
    const common = {
      label: normalizeTunnelLabel(input.label),
      ...endpoint,
      enabled: input.enabled ?? true,
      options: normalizeTunnelOptions(tunnelProtocol, input.options, currentOptions),
    }
    if (tunnelProtocol === 'http') {
      return {
        protocol: tunnelProtocol,
        customDomains: normalizeCustomDomains(input.customDomains, input.hostname),
        location: normalizeHttpLocation(input.location),
        serverPort: null,
        ...common,
      }
    }
    const selectedPort = input.serverPort == null ? this.availablePort(tunnelProtocol) : input.serverPort
    if (!Number.isSafeInteger(selectedPort) || selectedPort < this.portPool.start || selectedPort > this.portPool.end)
      throw new TunnelError('PORT_OUTSIDE_POOL', `Server port must be inside ${this.portPool.start}-${this.portPool.end}`)
    return { protocol: tunnelProtocol, serverPort: selectedPort, ...common }
  }

  createTunnel(clientId: string, input: TunnelMutationInput): TunnelDefinition {
    const id = randomUUID()
    const timestamp = now()
    const create = this.database.sqlite.transaction(() => {
      this.getClient(clientId)
      const value = this.values(input)
      try {
        this.database.sqlite.query(`
          INSERT INTO tunnels(id, client_internal_id, label, protocol, custom_domains, location, server_port, local_host, local_port, enabled, options_json, created_at, updated_at)
          VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        `).run(id, clientId, value.label, value.protocol, value.protocol === 'http' ? JSON.stringify(value.customDomains) : null, value.protocol === 'http' ? value.location : null, value.serverPort, value.localHost, value.localPort, value.enabled ? 1 : 0, JSON.stringify(value.options), timestamp, timestamp)
        if (value.protocol === 'http')
          this.reserveHttpRoutes(id, value.customDomains, value.location)
      }
      catch (cause) {
        constraintError(cause)
      }
      this.incrementDesiredRevision(clientId)
      const { clientId: _, ...created } = this.getTunnel(id)
      return created
    })
    const created = create.immediate()
    this.emit({ type: 'desired_state', clientId, ownerAccountId: this.getClient(clientId).ownerAccountId })
    return created
  }

  importTunnels(clientId: string, inputs: TunnelMutationInput[]): TunnelDefinition[] {
    if (!inputs.length)
      throw new TunnelError('INVALID_TUNNEL', 'Select at least one tunnel configuration to import')
    let ownerAccountId = ''
    const imported = this.database.sqlite.transaction(() => {
      ownerAccountId = this.getClient(clientId).ownerAccountId
      const created: TunnelDefinition[] = []
      try {
        for (const input of inputs) {
          if (input.protocol !== 'http' && input.serverPort == null)
            throw new TunnelError('INVALID_TUNNEL', 'Imported TCP and UDP tunnels require a server port')
          const id = randomUUID()
          const timestamp = now()
          const value = this.values({ ...input, enabled: false })
          this.database.sqlite.query(`
            INSERT INTO tunnels(id, client_internal_id, label, protocol, custom_domains, location, server_port, local_host, local_port, enabled, options_json, created_at, updated_at)
            VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
          `).run(id, clientId, value.label, value.protocol, value.protocol === 'http' ? JSON.stringify(value.customDomains) : null, value.protocol === 'http' ? value.location : null, value.serverPort, value.localHost, value.localPort, 0, JSON.stringify(value.options), timestamp, timestamp)
          if (value.protocol === 'http')
            this.reserveHttpRoutes(id, value.customDomains, value.location)
          const { clientId: _, ...tunnel } = this.getTunnel(id)
          created.push(tunnel)
        }
      }
      catch (cause) {
        constraintError(cause)
      }
      this.incrementDesiredRevision(clientId)
      return created
    })
    const created = imported.immediate()
    this.emit({ type: 'desired_state', clientId, ownerAccountId })
    return created
  }

  updateTunnel(id: string, patch: TunnelPatchInput): TunnelDefinition {
    let clientId = ''
    const update = this.database.sqlite.transaction(() => {
      const current = this.getTunnel(id)
      clientId = current.clientId
      const value = this.values({
        protocol: patch.protocol ?? current.protocol,
        customDomains: patch.customDomains ?? (patch.hostname === undefined && current.protocol === 'http' ? current.customDomains : undefined),
        hostname: patch.hostname,
        location: patch.location === undefined && current.protocol === 'http' ? current.location : patch.location,
        serverPort: patch.serverPort === undefined ? current.serverPort : patch.serverPort,
        localHost: patch.localHost ?? current.localHost,
        localPort: patch.localPort ?? current.localPort,
        enabled: patch.enabled ?? current.enabled,
        label: patch.label ?? current.label,
        options: patch.options,
      }, current.options)
      try {
        this.database.sqlite.query(`
          UPDATE tunnels SET label = ?, protocol = ?, custom_domains = ?, location = ?, server_port = ?, local_host = ?, local_port = ?, enabled = ?, options_json = ?, updated_at = ?
          WHERE id = ?
        `).run(value.label, value.protocol, value.protocol === 'http' ? JSON.stringify(value.customDomains) : null, value.protocol === 'http' ? value.location : null, value.serverPort, value.localHost, value.localPort, value.enabled ? 1 : 0, JSON.stringify(value.options), now(), id)
        this.database.sqlite.query('DELETE FROM tunnel_http_routes WHERE tunnel_id = ?').run(id)
        if (value.protocol === 'http')
          this.reserveHttpRoutes(id, value.customDomains, value.location)
      }
      catch (cause) {
        constraintError(cause)
      }
      this.incrementDesiredRevision(clientId)
      const { clientId: _, ...changed } = this.getTunnel(id)
      return changed
    })
    const changed = update.immediate()
    this.emit({ type: 'desired_state', clientId, ownerAccountId: this.getClient(clientId).ownerAccountId })
    return changed
  }

  deleteTunnel(id: string): void {
    let clientId = ''
    let ownerAccountId = ''
    const remove = this.database.sqlite.transaction(() => {
      const current = this.getTunnel(id)
      clientId = current.clientId
      ownerAccountId = this.getClient(clientId).ownerAccountId
      this.database.sqlite.query('DELETE FROM tunnels WHERE id = ?').run(id)
      this.incrementDesiredRevision(clientId)
    })
    remove.immediate()
    this.emit({ type: 'desired_state', clientId, ownerAccountId })
  }

  recordAppliedRevision(clientId: string, revision: number): void {
    if (!Number.isSafeInteger(revision) || revision < 0)
      throw new TunnelError('INVALID_REVISION', 'Applied Revision must be a non-negative integer')
    const client = this.getClient(clientId)
    if (revision > client.desiredRevision)
      throw new TunnelError('INVALID_REVISION', 'Applied Revision cannot exceed Desired Revision')
    if (revision > client.lastAppliedRevision) {
      this.database.sqlite.query('UPDATE clients SET last_applied_revision = ? WHERE internal_id = ?').run(revision, clientId)
      this.emit({ type: 'client_updated', clientId, ownerAccountId: this.getClient(clientId).ownerAccountId })
    }
  }
}
