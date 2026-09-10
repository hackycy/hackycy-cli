import type { TunnelSnapshot } from '../types'
import type { ClientFrpRuntime } from './reconciler'
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, test } from 'bun:test'
import { ClientReconciler } from './reconciler'
import { activeFrpcConfigPath } from './state'

const roots: string[] = []

afterEach(async () => {
  await Promise.all(roots.splice(0).map(root => rm(root, { recursive: true, force: true })))
})

function snapshot(revision: number, localPort: number, enabled = true): TunnelSnapshot {
  return {
    clientKey: 'client',
    revision,
    tunnels: [{ id: 'tunnel', label: '', protocol: 'http', customDomains: ['app.example.com'], location: '/app', serverPort: null, localHost: '127.0.0.1', localPort, enabled, options: { transport: { useEncryption: false, useCompression: false, bandwidthLimit: null, proxyProtocolVersion: null }, healthCheck: null, http: { basicAuth: null, hostHeaderRewrite: null, requestHeaders: [], responseHeaders: [] } }, createdAt: '', updatedAt: '' }],
  }
}

class FakeRuntime implements ClientFrpRuntime {
  operations: string[] = []
  failVerify = false
  failStart = false
  running = false
  verifyGate?: Promise<void>

  async verify(): Promise<void> {
    this.operations.push('verify')
    await this.verifyGate
    if (this.failVerify)
      throw new Error('invalid candidate')
  }

  async start(): Promise<void> {
    this.operations.push('start')
    if (this.failStart) {
      this.failStart = false
      throw new Error('startup failed')
    }
    this.running = true
  }

  async stop(): Promise<void> {
    this.operations.push('stop')
    this.running = false
  }

  async restart(): Promise<void> {
    this.operations.push('restart')
  }
}

async function fixture(): Promise<{ root: string, runtime: FakeRuntime, reconciler: ClientReconciler }> {
  const root = await mkdtemp(path.join(tmpdir(), 'ycy-tunnel-reconcile-'))
  roots.push(root)
  const runtime = new FakeRuntime()
  return { root, runtime, reconciler: new ClientReconciler(root, runtime) }
}

describe('ClientReconciler', () => {
  test('validates, activates, and persists Applied Revision', async () => {
    const { root, runtime, reconciler } = await fixture()
    await reconciler.apply({ advertisedFrpHost: 'server', advertisedFrpPort: 7000, internalFrpToken: 'secret', snapshot: snapshot(1, 3000) })
    expect(runtime.operations).toEqual(['verify', 'stop', 'start'])
    expect((await reconciler.lastApplied())?.revision).toBe(1)
    expect(await readFile(activeFrpcConfigPath(root), 'utf8')).toContain('localPort = 3000')
    expect((await readdir(root)).filter(name => name.includes('.candidate'))).toEqual([])
  })

  test('leaves the applied revision running after verification failure', async () => {
    const { root, runtime, reconciler } = await fixture()
    await reconciler.apply({ advertisedFrpHost: 'server', advertisedFrpPort: 7000, internalFrpToken: 'secret', snapshot: snapshot(1, 3000) })
    runtime.operations = []
    runtime.failVerify = true
    await expect(reconciler.apply({ advertisedFrpHost: 'server', advertisedFrpPort: 7000, internalFrpToken: 'secret', snapshot: snapshot(2, 4000) })).rejects.toThrow('invalid candidate')
    expect(runtime.operations).toEqual(['verify'])
    expect((await reconciler.lastApplied())?.revision).toBe(1)
    expect(await readFile(activeFrpcConfigPath(root), 'utf8')).toContain('localPort = 3000')
  })

  test('rolls back the previous child and file after activation failure', async () => {
    const { root, runtime, reconciler } = await fixture()
    await reconciler.apply({ advertisedFrpHost: 'server', advertisedFrpPort: 7000, internalFrpToken: 'secret', snapshot: snapshot(1, 3000) })
    runtime.operations = []
    runtime.failStart = true
    await expect(reconciler.apply({ advertisedFrpHost: 'server', advertisedFrpPort: 7000, internalFrpToken: 'secret', snapshot: snapshot(2, 4000) })).rejects.toThrow('startup failed')
    expect(runtime.operations).toEqual(['verify', 'stop', 'start', 'stop', 'start'])
    expect((await reconciler.lastApplied())?.revision).toBe(1)
    expect(await readFile(activeFrpcConfigPath(root), 'utf8')).toContain('localPort = 3000')
  })

  test('acknowledges an empty enabled set without retaining a child', async () => {
    const { runtime, reconciler } = await fixture()
    await reconciler.apply({ advertisedFrpHost: 'server', advertisedFrpPort: 7000, internalFrpToken: 'secret', snapshot: snapshot(1, 3000, false) })
    expect(runtime.operations).toEqual(['stop'])
    expect((await reconciler.lastApplied())?.revision).toBe(1)
  })

  test('does not restart an active Applied Revision on control-link reconnect', async () => {
    const { runtime, reconciler } = await fixture()
    const input = { advertisedFrpHost: 'server', advertisedFrpPort: 7000, internalFrpToken: 'secret', snapshot: snapshot(1, 3000) }
    await reconciler.apply(input)
    runtime.operations = []
    await reconciler.apply(input)
    expect(runtime.operations).toEqual([])
  })

  test('authenticates and activates before using the same cached revision in a new process', async () => {
    const { root, reconciler } = await fixture()
    const input = { advertisedFrpHost: 'server', advertisedFrpPort: 7000, internalFrpToken: 'secret', snapshot: snapshot(1, 3000) }
    await reconciler.apply(input)
    const freshRuntime = new FakeRuntime()
    const freshProcess = new ClientReconciler(root, freshRuntime)
    expect(freshRuntime.operations).toEqual([])
    await freshProcess.apply(input)
    expect(freshRuntime.operations).toEqual(['verify', 'stop', 'start'])
  })

  test('cancels an in-flight apply before starting a child when the client stops', async () => {
    const { root, runtime, reconciler } = await fixture()
    await reconciler.apply({ advertisedFrpHost: 'server', advertisedFrpPort: 7000, internalFrpToken: 'secret', snapshot: snapshot(1, 3000) })
    runtime.operations = []
    let release!: () => void
    runtime.verifyGate = new Promise<void>(resolve => release = resolve)
    const applying = reconciler.apply({ advertisedFrpHost: 'server', advertisedFrpPort: 7000, internalFrpToken: 'secret', snapshot: snapshot(2, 4000) })
    for (let attempt = 0; attempt < 20 && !runtime.operations.includes('verify'); attempt++)
      await Bun.sleep(1)
    const stopping = reconciler.stop()
    release()
    await expect(applying).rejects.toThrow('stopping')
    await stopping
    expect(runtime.operations).toEqual(['verify', 'stop'])
    expect(runtime.running).toBe(false)
    expect((await reconciler.lastApplied())?.revision).toBe(1)
    expect(await readFile(activeFrpcConfigPath(root), 'utf8')).toContain('localPort = 3000')
    expect((await readdir(root)).filter(name => name.includes('.candidate'))).toEqual([])
  })
})
