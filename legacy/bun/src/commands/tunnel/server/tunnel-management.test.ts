import type { FrpChild } from '../frp/supervisor'
import type { ServerTunnelConfig } from '../types'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { FrpSupervisor } from '../frp/supervisor'
import { TunnelError } from '../types'
import { AgentGateway } from './agent-gateway'
import { TunnelControlPlane } from './control-plane'
import { TunnelDatabase } from './database'
import { FrpsConfiguration } from './frps-configuration'
import { TunnelManagement } from './tunnel-management'

class FakeChild implements FrpChild {
  readonly pid = 42
  readonly exited: Promise<number>
  private exit!: (code: number) => void

  constructor() {
    this.exited = new Promise(resolve => this.exit = resolve)
  }

  kill(): void {
    this.exit(0)
  }
}

interface Fixture {
  database: TunnelDatabase
  gateway: AgentGateway
  frps: FrpSupervisor
  management: TunnelManagement
  sessionDirectory: string
}

const fixtures: Fixture[] = []

function serverConfig(overrides: Partial<ServerTunnelConfig> = {}): ServerTunnelConfig {
  return {
    address: '127.0.0.1',
    controlPort: 7500,
    frpPort: 7000,
    httpPort: 8080,
    portRange: { start: 20000, end: 20002 },
    dataDir: '/data',
    adminUser: 'admin',
    adminPassword: 'environment-secret',
    ...overrides,
  }
}

afterEach(async () => {
  for (const fixture of fixtures.splice(0)) {
    fixture.management.stop()
    fixture.gateway.stop()
    await fixture.frps.stop()
    fixture.database.close()
    await rm(fixture.sessionDirectory, { recursive: true, force: true })
  }
})

async function fixture(options: { sessionLifetimeMs?: number, adminPassword?: string } = {}): Promise<Fixture> {
  const database = new TunnelDatabase(':memory:')
  const controlPlane = new TunnelControlPlane(database, { start: 20000, end: 20002 })
  const gateway = new AgentGateway(controlPlane, 7000, 'internal')
  const frps = new FrpSupervisor({ binaryPath: '/frps', role: 'frps', spawn: () => new FakeChild() })
  const config = serverConfig({ adminPassword: options.adminPassword ?? 'environment-secret' })
  const sessionDirectory = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-management-session-'))
  const management = await TunnelManagement.create({
    database,
    controlPlane,
    gateway,
    frps,
    frpsConfiguration: new FrpsConfiguration(config),
    ...(options.sessionLifetimeMs === undefined ? {} : { sessionLifetimeMs: options.sessionLifetimeMs }),
    sessionDirectory,
    serverConfig: config,
  })
  const value = { database, gateway, frps, management, sessionDirectory }
  fixtures.push(value)
  return value
}

async function users(management: TunnelManagement): Promise<{
  environment: ReturnType<TunnelManagement['resume']> & {}
  alice: ReturnType<TunnelManagement['resume']> & {}
  bob: ReturnType<TunnelManagement['resume']> & {}
}> {
  const environment = management.resume((await management.signIn({ username: 'admin', password: 'environment-secret' })).token)!
  const administration = environment.administration()
  await administration.createAccount({ username: 'alice', password: 'alice-secret', role: 'user' })
  await administration.createAccount({ username: 'bob', password: 'bob-secret', role: 'user' })
  return {
    environment,
    alice: management.resume((await management.signIn({ username: 'ALICE', password: 'alice-secret' })).token)!,
    bob: management.resume((await management.signIn({ username: 'bob', password: 'bob-secret' })).token)!,
  }
}

describe('TunnelManagement ownership', () => {
  test('binds new clients to their creator and hides them from other users', async () => {
    const { management } = await fixture()
    const { environment, alice, bob } = await users(management)
    const client = await alice.createClient({ remark: 'Alice gateway' })

    expect((await alice.listClients()).map(value => value.id)).toEqual([client.id])
    expect(await bob.listClients()).toEqual([])
    expect(() => bob.getClient(client.id)).toThrow(new TunnelError('NOT_FOUND', 'Trusted Tunnel Client was not found'))
    expect((await environment.listClients()).map(value => value.id)).toEqual([client.id])
    expect((await environment.getClient(client.id)).client.owner.username).toBe('alice')
  })

  test('authorizes the complete client and tunnel workflow by owner', async () => {
    const { management } = await fixture()
    const { environment, alice, bob } = await users(management)
    const client = alice.createClient({ remark: 'Alice gateway' })

    for (const operation of [
      () => bob.updateClientRemark(client.id, 'stolen'),
      () => bob.rotateClientToken(client.id),
      () => bob.restartClient(client.id),
      () => bob.createTunnel(client.id, { protocol: 'http', hostname: 'stolen.example.com', localPort: 3000 }),
      () => bob.deleteClient(client.id),
    ])
      expect(operation).toThrow('not found')
    expect(alice.getClient(client.id)).toMatchObject({
      client: { remark: 'Alice gateway', token: client.token },
      tunnels: [],
    })

    const updated = alice.updateClientRemark(client.id, 'Alice office gateway')
    expect(updated.remark).toBe('Alice office gateway')
    const rotated = alice.rotateClientToken(client.id)
    expect(rotated.token).not.toBe(client.token)
    expect(() => alice.restartClient(client.id)).toThrow('not connected')

    const tunnel = alice.createTunnel(client.id, { protocol: 'http', hostname: 'alice.example.com', localPort: 3000 })
    expect(() => bob.updateTunnel(tunnel.id, { enabled: false })).toThrow('not found')
    expect(() => bob.deleteTunnel(tunnel.id)).toThrow('not found')
    const administratorTunnel = environment.createTunnel(client.id, { protocol: 'tcp', localPort: 5432 })
    expect(environment.getClient(client.id).client.owner.username).toBe('alice')
    expect(environment.updateTunnel(administratorTunnel.id, { enabled: false }).enabled).toBe(false)
    environment.deleteTunnel(administratorTunnel.id)
    expect(alice.updateTunnel(tunnel.id, { enabled: false }).enabled).toBe(false)
    alice.deleteTunnel(tunnel.id)
    alice.deleteClient(client.id)
    expect(alice.listClients()).toEqual([])
  })

  test('keeps resource reservations global without revealing another owner', async () => {
    const { management } = await fixture()
    const { alice, bob } = await users(management)
    const aliceClient = alice.createClient({ remark: 'Alice' })
    const bobClient = bob.createClient({ remark: 'Bob' })
    alice.createTunnel(aliceClient.id, { protocol: 'http', hostname: 'shared.example.com', localPort: 3000 })

    try {
      bob.createTunnel(bobClient.id, { protocol: 'http', hostname: 'shared.example.com', localPort: 4000 })
      throw new Error('Expected the hostname reservation to fail')
    }
    catch (cause) {
      expect(cause).toBeInstanceOf(TunnelError)
      expect((cause as TunnelError).code).toBe('RESOURCE_RESERVED')
      expect((cause as Error).message).toBe('HTTP Tunnel custom domain and location are already reserved')
      expect((cause as Error).message).not.toContain('alice')
    }
  })
})

describe('TunnelManagement accounts and sessions', () => {
  test('accepts a five-character environment administrator password', async () => {
    const { management } = await fixture({ adminPassword: '12345' })

    expect((await management.signIn({ username: 'admin', password: '12345' })).account.username).toBe('admin')
  })

  test('actively expires an idle session and its event stream', async () => {
    const { management } = await fixture({ sessionLifetimeMs: 50 })
    const grant = await management.signIn({ username: 'admin', password: 'environment-secret' })
    const workspace = management.resume(grant.token)!
    expect(Date.parse(workspace.sessionExpiresAt)).toBeGreaterThan(Date.now())
    const events: string[] = []
    await new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error('session did not expire')), 1_000)
      workspace.observe((event) => {
        events.push(event)
        if (event === 'session_revoked') {
          clearTimeout(timeout)
          resolve()
        }
      })
    })

    expect(events).toEqual(['session_revoked'])
    expect(management.resume(grant.token)).toBeUndefined()
  })

  test('enforces account lifecycle rules and immediately revokes changed sessions', async () => {
    const { management } = await fixture()
    const { environment, alice } = await users(management)
    const administration = environment.administration()

    expect(() => alice.administration()).toThrow('Administrator role is required')
    expect((await administration.createAccount({ username: '-operator', password: 'operator-secret' })).username).toBe('-operator')
    await expect(administration.createAccount({ username: 'short-password', password: 'tiny' })).rejects.toThrow('Password must contain 5-256 characters')
    expect((await administration.createAccount({ username: 'minimum-password', password: '12345' })).username).toBe('minimum-password')
    await expect(administration.createAccount({ username: ' alice ', password: 'another-secret' })).rejects.toThrow('Username must contain')
    await expect(administration.createAccount({ username: 'Alice', password: 'another-secret' })).rejects.toThrow('already in use')
    expect(() => administration.changeAccountRole(environment.account.id, 'user')).toThrow('managed by environment')
    await expect(administration.resetAccountPassword(environment.account.id, 'replacement-secret')).rejects.toThrow('managed by environment')
    expect(() => administration.deleteAccount(environment.account.id)).toThrow('managed by environment')

    const aliceClient = alice.createClient({ remark: 'Owned resource' })
    const aliceAccount = administration.listAccounts().find(account => account.username === 'alice')!
    expect(aliceAccount.clientCount).toBe(1)
    expect(() => administration.deleteAccount(aliceAccount.id)).toThrow('still owns')

    administration.changeAccountRole(aliceAccount.id, 'admin')
    expect(() => alice.listClients()).toThrow('Authenticated session is required')
    const promotedGrant = await management.signIn({ username: 'alice', password: 'alice-secret' })
    const promoted = management.resume(promotedGrant.token)!
    expect(promoted.account.role).toBe('admin')
    promoted.deleteClient(aliceClient.id)

    const bobGrant = await management.signIn({ username: 'bob', password: 'bob-secret' })
    const currentBob = management.resume(bobGrant.token)!
    await expect(currentBob.changePassword({ currentPassword: 'bob-secret', newPassword: 'tiny' })).rejects.toThrow('Password must contain 5-256 characters')
    await expect(currentBob.changePassword({ currentPassword: 'wrong-secret', newPassword: 'bob-replacement' })).rejects.toThrow('Current password is invalid')
    await currentBob.changePassword({ currentPassword: 'bob-secret', newPassword: '12345' })
    expect(management.resume(bobGrant.token)).toBeUndefined()
    await expect(management.signIn({ username: 'bob', password: 'bob-secret' })).rejects.toThrow('credentials are invalid')
    expect((await management.signIn({ username: 'bob', password: '12345' })).account.username).toBe('bob')
    const resetGrant = await management.signIn({ username: 'bob', password: '12345' })
    const resetBob = management.resume(resetGrant.token)!
    await expect(administration.resetAccountPassword(resetBob.account.id, 'tiny')).rejects.toThrow('Password must contain 5-256 characters')
    await administration.resetAccountPassword(resetBob.account.id, 'abcde')
    expect(management.resume(resetGrant.token)).toBeUndefined()
    await expect(management.signIn({ username: 'bob', password: '12345' })).rejects.toThrow('credentials are invalid')
    expect((await management.signIn({ username: 'bob', password: 'abcde' })).account.username).toBe('bob')

    administration.deleteAccount(aliceAccount.id)
    expect(management.resume(promotedGrant.token)).toBeUndefined()
    await expect(management.signIn({ username: 'alice', password: 'alice-secret' })).rejects.toThrow('credentials are invalid')
  })

  test('keeps the environment administrator identity stable and rejects username collisions on restart', async () => {
    const first = await fixture()
    const environment = first.management.resume((await first.management.signIn({ username: 'admin', password: 'environment-secret' })).token)!
    const client = environment.createClient({ remark: 'Stable owner' })
    const originalOwnerId = client.owner.id
    first.management.stop()
    first.gateway.stop()

    const secondControlPlane = new TunnelControlPlane(first.database, { start: 20000, end: 20002 })
    const secondGateway = new AgentGateway(secondControlPlane, 7000, 'internal')
    const secondFrps = new FrpSupervisor({ binaryPath: '/frps', role: 'frps', spawn: () => new FakeChild() })
    const secondConfig = serverConfig({ adminUser: 'root-admin', adminPassword: 'replacement-environment-secret' })
    const second = await TunnelManagement.create({
      database: first.database,
      controlPlane: secondControlPlane,
      gateway: secondGateway,
      frps: secondFrps,
      frpsConfiguration: new FrpsConfiguration(secondConfig),
      sessionDirectory: first.sessionDirectory,
      serverConfig: secondConfig,
    })
    const renamed = second.resume((await second.signIn({ username: 'ROOT-ADMIN', password: 'replacement-environment-secret' })).token)!
    expect(renamed.account.id).toBe(originalOwnerId)
    expect(renamed.listClients()[0]?.owner).toEqual({ id: originalOwnerId, username: 'root-admin' })
    await renamed.administration().createAccount({ username: 'collision', password: 'collision-secret' })
    second.stop()
    secondGateway.stop()
    await secondFrps.stop()

    const thirdControlPlane = new TunnelControlPlane(first.database, { start: 20000, end: 20002 })
    const thirdGateway = new AgentGateway(thirdControlPlane, 7000, 'internal')
    const thirdFrps = new FrpSupervisor({ binaryPath: '/frps', role: 'frps', spawn: () => new FakeChild() })
    const thirdConfig = serverConfig({ adminUser: 'collision', adminPassword: 'another-environment-secret' })
    await expect(TunnelManagement.create({
      database: first.database,
      controlPlane: thirdControlPlane,
      gateway: thirdGateway,
      frps: thirdFrps,
      frpsConfiguration: new FrpsConfiguration(thirdConfig),
      sessionDirectory: first.sessionDirectory,
      serverConfig: thirdConfig,
    })).rejects.toThrow('conflicts with a local account')
    thirdGateway.stop()
    await thirdFrps.stop()
  })

  test('restores a persisted session only while the environment credentials are unchanged', async () => {
    const first = await fixture()
    const grant = await first.management.signIn({ username: 'admin', password: 'environment-secret' })
    first.management.stop()
    first.gateway.stop()

    const restoredControlPlane = new TunnelControlPlane(first.database, { start: 20000, end: 20002 })
    const restoredGateway = new AgentGateway(restoredControlPlane, 7000, 'internal')
    const restoredFrps = new FrpSupervisor({ binaryPath: '/frps', role: 'frps', spawn: () => new FakeChild() })
    const restoredConfig = serverConfig()
    const restored = await TunnelManagement.create({
      database: first.database,
      controlPlane: restoredControlPlane,
      gateway: restoredGateway,
      frps: restoredFrps,
      frpsConfiguration: new FrpsConfiguration(restoredConfig),
      sessionDirectory: first.sessionDirectory,
      serverConfig: restoredConfig,
    })
    expect(restored.resume(grant.token)?.account.username).toBe('admin')
    restored.stop()
    restoredGateway.stop()
    await restoredFrps.stop()

    const changedControlPlane = new TunnelControlPlane(first.database, { start: 20000, end: 20002 })
    const changedGateway = new AgentGateway(changedControlPlane, 7000, 'internal')
    const changedFrps = new FrpSupervisor({ binaryPath: '/frps', role: 'frps', spawn: () => new FakeChild() })
    const changedConfig = serverConfig({ adminPassword: 'replacement-environment-secret' })
    const changed = await TunnelManagement.create({
      database: first.database,
      controlPlane: changedControlPlane,
      gateway: changedGateway,
      frps: changedFrps,
      frpsConfiguration: new FrpsConfiguration(changedConfig),
      sessionDirectory: first.sessionDirectory,
      serverConfig: changedConfig,
    })
    expect(changed.resume(grant.token)).toBeUndefined()
    changed.stop()
    changedGateway.stop()
    await changedFrps.stop()
  })
})

describe('TunnelManagement projections and events', () => {
  test('scopes overview and invalidation events while reserving server control for admins', async () => {
    const { management } = await fixture()
    const { environment, alice, bob } = await users(management)
    const adminEvents: string[] = []
    const aliceEvents: string[] = []
    const bobEvents: string[] = []
    environment.observe(event => adminEvents.push(event))
    alice.observe(event => aliceEvents.push(event))
    bob.observe(event => bobEvents.push(event))

    const aliceClient = alice.createClient({ remark: 'Alice' })
    alice.createTunnel(aliceClient.id, { protocol: 'http', hostname: 'alice.example.com', localPort: 3000 })
    bob.createClient({ remark: 'Bob' })

    expect((await alice.overview()).counts).toMatchObject({ clients: 1, tunnels: 1 })
    expect((await bob.overview()).counts).toMatchObject({ clients: 1, tunnels: 0 })
    expect((await environment.overview()).counts).toMatchObject({ clients: 2, tunnels: 1 })
    expect((await alice.overview()).server).toBeUndefined()
    expect((await environment.overview()).server?.settings.adminUser).toBe('admin')
    expect(aliceEvents).toEqual(['changed', 'changed'])
    expect(bobEvents).toEqual(['changed'])
    expect(adminEvents).toEqual(['changed', 'changed', 'changed'])

    await environment.administration().createAccount({ username: 'charlie', password: 'charlie-secret' })
    expect(aliceEvents).toHaveLength(2)
    expect(bobEvents).toHaveLength(1)
    expect(adminEvents).toHaveLength(4)

    await environment.administration().controlFrps('start')
    expect((await environment.overview()).server?.frps.state).toBe('running')
    expect(adminEvents.at(-1)).toBe('changed')
    expect(aliceEvents).toHaveLength(2)

    await environment.administration().frpsConfiguration().setCustom404Page('')
    expect(adminEvents.at(-1)).toBe('changed')
    expect(aliceEvents).toHaveLength(2)
    expect(bobEvents).toHaveLength(1)

    const aliceAccount = environment.administration().listAccounts().find(account => account.username === 'alice')!
    environment.administration().changeAccountRole(aliceAccount.id, 'admin')
    expect(aliceEvents.at(-1)).toBe('session_revoked')
  })
})
