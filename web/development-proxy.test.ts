import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { createServer } from 'node:http'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { developmentApps } from './vite.config'

interface DevelopmentApp {
  mode: 'diff' | 'fs' | 'tunnel-server'
  defaultPort: number
  shellPaths: string[]
  title: string
  proxyPaths: string[]
}

const apps: DevelopmentApp[] = [
  { mode: 'diff', defaultPort: 5173, shellPaths: ['/', '/deep/link'], title: 'HACKYCY CLI — DIFF SERVER', proxyPaths: ['/api/fixture', '/mcp/fixture'] },
  { mode: 'fs', defaultPort: 5174, shellPaths: ['/', '/browse/documents'], title: 'HACKYCY CLI - FILE BROWSER', proxyPaths: ['/api/fixture', '/files/fixture', '/thumbnails/fixture'] },
  { mode: 'tunnel-server', defaultPort: 5175, shellPaths: ['/', '/clients/client-1'], title: 'HACKYCY CLI - TUNNEL CONTROL PLANE', proxyPaths: ['/api/fixture'] },
]

describe.sequential('development proxy modes', () => {
  for (const app of apps) {
    it(`serves ${app.mode} through its strict port, HMR shell, and reserved proxies`, async () => {
      expect(developmentApps[app.mode].port).toBe(app.defaultPort)
      const port = await reservePort()
      const backend = createServer((request, response) => {
        response.setHeader('Content-Type', 'application/json')
        response.end(JSON.stringify({ host: request.headers.host, path: request.url }))
      })
      backend.listen(0, '127.0.0.1')
      await once(backend, 'listening')
      const address = backend.address()
      if (!address || typeof address === 'string')
        throw new Error('development proxy backend did not expose a TCP address')
      const backendURL = `http://127.0.0.1:${address.port}`
      const vite = startVite(app.mode, backendURL, port)

      try {
        await waitForServer(port)
        const second = startVite(app.mode, backendURL, port)
        const secondExit = await waitForExit(second, 5_000)
        expect(secondExit.code).toBe(1)

        for (const path of app.shellPaths) {
          const response = await request(port, path)
          expect(response.status).toBe(200)
          expect(response.headers.get('content-type')).toContain('text/html')
          const body = await response.text()
          expect(body).toContain(app.title)
          expect(body).toContain('/@vite/client')
        }

        for (const path of app.proxyPaths) {
          const response = await request(port, path)
          expect(response.status).toBe(200)
          await expect(response.json()).resolves.toEqual({ host: `127.0.0.1:${address.port}`, path })
        }

        expect((await request(port, '/assets/missing.js')).status).toBe(404)
        expect((await request(port, '/diff/index.html')).status).toBe(app.mode === 'diff' ? 200 : 404)
      }
      finally {
        await stopProcess(vite)
        await new Promise<void>(resolve => backend.close(() => resolve()))
      }
    }, 30_000)
  }
})

function startVite(mode: DevelopmentApp['mode'], backend: string, port: number) {
  return spawn(process.execPath, [resolve(import.meta.dirname, 'node_modules/vite/bin/vite.js'), '--mode', mode, '--host', '127.0.0.1', '--port', String(port)], {
    cwd: import.meta.dirname,
    env: { ...process.env, YCY_WEB_BACKEND: backend },
    stdio: 'pipe',
  })
}

async function waitForServer(port: number): Promise<void> {
  const timeout = Date.now() + 10_000
  while (Date.now() < timeout) {
    try {
      const response = await request(port, '/')
      if (response.status !== 502)
        return
    }
    catch {
      // The Vite process has not bound its strict port yet.
    }
    await delay(50)
  }
  throw new Error(`Vite development server did not listen on ${port}`)
}

async function waitForExit(process: ReturnType<typeof spawn>, timeout: number): Promise<{ code: number | null }> {
  const exit = once(process, 'exit').then(([code]) => ({ code: code as number | null }))
  return Promise.race([
    exit,
    delay(timeout).then(() => {
      process.kill('SIGTERM')
      throw new Error('second Vite process did not reject an occupied strict port')
    }),
  ])
}

async function stopProcess(process: ReturnType<typeof spawn>): Promise<void> {
  if (process.exitCode !== null)
    return
  const exit = once(process, 'exit')
  process.kill('SIGTERM')
  await Promise.race([exit, delay(5_000)])
}

async function reservePort(): Promise<number> {
  const server = createServer()
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  const address = server.address()
  if (!address || typeof address === 'string')
    throw new Error('could not reserve a local development port')
  await new Promise<void>(resolve => server.close(() => resolve()))
  return address.port
}

function request(port: number, path: string): Promise<Response> {
  return fetch(`http://127.0.0.1:${port}${path}`, { signal: AbortSignal.timeout(2_000) })
}

function delay(milliseconds: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, milliseconds))
}
