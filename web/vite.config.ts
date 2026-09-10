import type { Plugin } from 'vite'
import { resolve } from 'node:path'
import process from 'node:process'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

export const developmentApps = {
  'diff': { entry: '/diff/index.html', port: 5173, proxy: ['/api', '/mcp'] },
  'fs': { entry: '/fs/index.html', port: 5174, proxy: ['/api', '/files', '/thumbnails'] },
  'tunnel-server': { entry: '/tunnel-server/index.html', port: 5175, proxy: ['/api'] },
} as const

type AppMode = keyof typeof developmentApps

function selectedApp(mode: string): AppMode {
  return mode in developmentApps ? mode as AppMode : 'diff'
}

function isReservedRoute(mode: AppMode, pathname: string): boolean {
  return developmentApps[mode].proxy.some(prefix => pathname === prefix || pathname.startsWith(`${prefix}/`))
}

function isShellRoute(mode: AppMode, pathname: string): boolean {
  if (mode === 'diff')
    return true
  if (mode === 'fs')
    return pathname === '/' || pathname === '/browse' || pathname.startsWith('/browse/')
  return pathname === '/' || pathname === '/clients' || pathname.startsWith('/clients/') || pathname === '/accounts' || pathname === '/server'
}

function developmentShellPlugin(mode: AppMode): Plugin {
  const app = developmentApps[mode]
  return {
    name: `ycy-${mode}-development-shell`,
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        if (!request.url || (request.method !== 'GET' && request.method !== 'HEAD')) {
          next()
          return
        }
        const url = new URL(request.url, 'http://vite.local')
        if (isReservedRoute(mode, url.pathname) || url.pathname.startsWith('/assets/') || url.pathname.startsWith('/@') || url.pathname.startsWith('/__') || url.pathname.startsWith('/node_modules/')) {
          next()
          return
        }
        if (url.pathname.startsWith('/diff/') || url.pathname.startsWith('/fs/') || url.pathname.startsWith('/tunnel-server/')) {
          if (!url.pathname.startsWith(app.entry.slice(0, app.entry.lastIndexOf('/') + 1))) {
            response.statusCode = 404
            response.end()
            return
          }
          next()
          return
        }
        if (isShellRoute(mode, url.pathname))
          request.url = `${app.entry}${url.search}`
        next()
      })
    },
  }
}

export default defineConfig(({ mode }) => {
  const selected = selectedApp(mode)
  const app = developmentApps[selected]
  const backend = process.env.YCY_WEB_BACKEND ?? `http://127.0.0.1:${app.port + 1000}`
  const proxy = Object.fromEntries(app.proxy.map(path => [path, { target: backend, changeOrigin: true }]))

  return {
    plugins: [developmentShellPlugin(selected), react(), tailwindcss()],
    appType: 'mpa',
    base: '/',
    publicDir: false,
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      manifest: true,
      assetsDir: 'assets',
      assetsInlineLimit: 0,
      rollupOptions: {
        input: {
          diff: resolve(import.meta.dirname, 'diff/index.html'),
          fs: resolve(import.meta.dirname, 'fs/index.html'),
          tunnelServer: resolve(import.meta.dirname, 'tunnel-server/index.html'),
        },
      },
    },
    server: {
      port: app.port,
      strictPort: true,
      proxy,
    },
    test: {
      environment: 'node',
      include: ['**/*.test.ts', '**/*.test.tsx'],
    },
  }
})
