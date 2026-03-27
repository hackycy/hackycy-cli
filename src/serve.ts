import type { Dirent } from 'node:fs'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { cancel, intro, note, outro } from '@clack/prompts'
import ansis from 'ansis'
import { printTitle } from './utils'

export interface ServeOptions {
  directory: string
  port: number
  address: string
}

interface DirectoryEntry {
  name: string
  isDirectory: boolean
  size: number
  mtime: Date
  href: string
}

// ─── Utility Functions ────────────────────────────────────────────────────────

function formatFileSize(bytes: number): string {
  if (bytes === 0)
    return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return `${size.toFixed(i === 0 ? 0 : 1)} ${units[i]!}`
}

function formatDate(date: Date): string {
  const y = date.getFullYear()
  const mo = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  const h = String(date.getHours()).padStart(2, '0')
  const mi = String(date.getMinutes()).padStart(2, '0')
  return `${y}-${mo}-${d} ${h}:${mi}`
}

function escapeHtml(str: string): string {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

// ─── Security ─────────────────────────────────────────────────────────────────

async function resolveSafePath(root: string, urlPath: string): Promise<string | null> {
  let decoded: string
  try {
    decoded = decodeURIComponent(urlPath)
  }
  catch {
    return null
  }

  const candidate = path.resolve(root, decoded.replace(/^\/+/, ''))
  const rootWithSep = root.endsWith(path.sep) ? root : root + path.sep
  const isWithinRoot = candidate === root || candidate.startsWith(rootWithSep)

  if (!isWithinRoot)
    return null

  try {
    const realCandidate = await fs.realpath(candidate)
    const realRoot = await fs.realpath(root)
    const realRootWithSep = realRoot.endsWith(path.sep) ? realRoot : realRoot + path.sep
    const realIsWithin = realCandidate === realRoot || realCandidate.startsWith(realRootWithSep)
    if (!realIsWithin)
      return null
  }
  catch {
    // Path doesn't exist yet — caller's fs.stat will produce the 404
  }

  return candidate
}

// ─── File Serving ─────────────────────────────────────────────────────────────

function isInlineMimeType(mimeType: string): boolean {
  const base = mimeType.split(';')[0]!.trim().toLowerCase()
  const [type, subtype] = base.split('/')
  if (!type || !subtype)
    return false
  if (['text', 'image', 'video', 'audio'].includes(type))
    return true
  if (type === 'application')
    return ['pdf', 'json', 'xml', 'javascript', 'xhtml+xml', 'atom+xml', 'rss+xml', 'ld+json'].includes(subtype)
  return false
}

async function serveFile(filePath: string, stat: Awaited<ReturnType<typeof fs.stat>>): Promise<Response> {
  const file = Bun.file(filePath)
  const mimeType = file.type || 'application/octet-stream'
  const encoded = encodeURIComponent(path.basename(filePath)).replace(/'/g, '%27')
  const disposition = isInlineMimeType(mimeType)
    ? `inline; filename="${encoded}"; filename*=UTF-8''${encoded}`
    : `attachment; filename="${encoded}"; filename*=UTF-8''${encoded}`
  return new Response(file, {
    headers: {
      'Content-Type': mimeType,
      'Content-Disposition': disposition,
      'Content-Length': String(stat.size),
      'Last-Modified': stat.mtime.toUTCString(),
    },
  })
}

// ─── HTML Template ────────────────────────────────────────────────────────────

function buildBreadcrumb(urlPath: string): string {
  const parts = urlPath.split('/').filter(Boolean)
  let html = `<a href="/">/</a>`
  let accumulated = ''
  for (const part of parts) {
    accumulated += `/${encodeURIComponent(part)}`
    html += `&nbsp;/&nbsp;<a href="${accumulated}/">${escapeHtml(decodeURIComponent(part))}</a>`
  }
  return html
}

function buildDirectoryHtml(urlPath: string, entries: DirectoryEntry[]): string {
  const isRoot = urlPath === '/'
  const title = `Index of ${escapeHtml(urlPath)}`
  const breadcrumb = buildBreadcrumb(urlPath)

  const parentHref = urlPath.replace(/[^/]+\/$/, '') || '/'
  const parentRow = isRoot
    ? ''
    : `<tr class="parent-row">
        <td class="name-cell parent-link" colspan="3">
          <span class="icon">&#x2B06;</span>
          <a href="${parentHref}">Parent directory</a>
        </td>
      </tr>`

  const entryRows = entries.map((e) => {
    const icon = e.isDirectory ? '&#x1F4C1;' : '&#x1F4C4;'
    const sizeStr = e.isDirectory ? '-' : formatFileSize(e.size)
    const dateStr = formatDate(e.mtime)
    const nameClass = e.isDirectory ? 'dir-link' : 'file-link'
    return `<tr>
      <td class="name-cell ${nameClass}">
        <span class="icon">${icon}</span>
        <a href="${e.href}">${escapeHtml(e.name)}${e.isDirectory ? '/' : ''}</a>
      </td>
      <td class="size-col">${sizeStr}</td>
      <td class="date-col">${dateStr}</td>
    </tr>`
  }).join('\n')

  const emptyRow = entries.length === 0
    ? '<tr><td colspan="3" class="empty-state">Empty directory</td></tr>'
    : ''

  return `<!DOCTYPE html>
<html lang="en" data-theme="dark">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>${title}</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
    :root {
      --bg: #0f1117;
      --fg: #e2e8f0;
      --border: #2d3748;
      --hover-bg: #1e293b;
      --row-border: #1e293b;
      --brand: #22d3ee;
      --title: #f1f5f9;
      --breadcrumb: #94a3b8;
      --link: #38bdf8;
      --muted: #64748b;
      --file-fg: #e2e8f0;
      --footer: #334155;
      --empty: #475569;
      --btn-bg: #1e293b;
      --btn-fg: #94a3b8;
      --btn-border: #2d3748;
    }
    [data-theme="light"] {
      --bg: #f8fafc;
      --fg: #1e293b;
      --border: #e2e8f0;
      --hover-bg: #f1f5f9;
      --row-border: #e2e8f0;
      --brand: #0891b2;
      --title: #0f172a;
      --breadcrumb: #64748b;
      --link: #0284c7;
      --muted: #94a3b8;
      --file-fg: #334155;
      --footer: #94a3b8;
      --empty: #94a3b8;
      --btn-bg: #e2e8f0;
      --btn-fg: #475569;
      --btn-border: #cbd5e1;
    }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: var(--bg);
      color: var(--fg);
      padding: 2rem;
      min-height: 100vh;
      font-size: 14px;
      transition: background 0.2s, color 0.2s;
    }
    .header {
      margin-bottom: 1.5rem;
      padding-bottom: 1rem;
      border-bottom: 1px solid var(--border);
      display: flex;
      align-items: flex-start;
      justify-content: space-between;
      gap: 1rem;
    }
    .header-left { flex: 1; min-width: 0; }
    .brand {
      font-size: 0.7rem;
      font-weight: 700;
      letter-spacing: 0.15em;
      text-transform: uppercase;
      color: var(--brand);
      margin-bottom: 0.5rem;
    }
    .page-title {
      font-size: 1.1rem;
      font-weight: 600;
      color: var(--title);
      word-break: break-all;
    }
    .breadcrumb {
      margin-top: 0.5rem;
      font-size: 0.8rem;
      color: var(--breadcrumb);
    }
    .breadcrumb a { color: var(--link); text-decoration: none; }
    .breadcrumb a:hover { text-decoration: underline; }
    .theme-btn {
      flex-shrink: 0;
      margin-top: 0.1rem;
      padding: 0.35rem 0.65rem;
      background: var(--btn-bg);
      color: var(--btn-fg);
      border: 1px solid var(--btn-border);
      border-radius: 6px;
      cursor: pointer;
      font-size: 0.8rem;
      line-height: 1;
      transition: background 0.15s, color 0.15s, border-color 0.15s;
      white-space: nowrap;
    }
    .theme-btn:hover { opacity: 0.8; }
    table { width: 100%; border-collapse: collapse; }
    thead th {
      text-align: left;
      padding: 0.5rem 0.75rem;
      color: var(--muted);
      font-size: 0.7rem;
      font-weight: 700;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      border-bottom: 1px solid var(--border);
    }
    thead th.right { text-align: right; }
    tbody tr { transition: background 0.1s; }
    tbody tr:hover { background: var(--hover-bg); }
    tbody td {
      padding: 0.55rem 0.75rem;
      border-bottom: 1px solid var(--row-border);
      vertical-align: middle;
    }
    .name-cell { display: flex; align-items: center; gap: 0.5rem; }
    .icon { flex-shrink: 0; }
    .dir-link a { color: var(--link); text-decoration: none; font-weight: 500; }
    .dir-link a:hover { text-decoration: underline; }
    .file-link a { color: var(--file-fg); text-decoration: none; }
    .file-link a:hover { color: var(--link); text-decoration: underline; }
    .parent-link a { color: var(--muted); text-decoration: none; }
    .parent-link a:hover { color: var(--breadcrumb); text-decoration: underline; }
    .size-col {
      color: var(--muted);
      text-align: right;
      white-space: nowrap;
      font-variant-numeric: tabular-nums;
    }
    .date-col {
      color: var(--muted);
      text-align: right;
      white-space: nowrap;
      font-size: 0.8rem;
      font-variant-numeric: tabular-nums;
    }
    .footer { margin-top: 1.25rem; font-size: 0.72rem; color: var(--footer); }
    .empty-state { padding: 2rem; text-align: center; color: var(--empty); font-style: italic; }
  </style>
  <script>
    (function() {
      var saved = localStorage.getItem('theme');
      if (saved === 'light') document.documentElement.setAttribute('data-theme', 'light');
    })();
  </script>
</head>
<body>
  <div class="header">
    <div class="header-left">
      <div class="brand">HACKYCY CLI &mdash; File Server</div>
      <div class="page-title">${title}</div>
      <div class="breadcrumb">${breadcrumb}</div>
    </div>
    <button class="theme-btn" id="themeBtn" onclick="toggleTheme()" title="Toggle theme">&#9790;</button>
  </div>
  <script>
    function toggleTheme() {
      var html = document.documentElement;
      var btn = document.getElementById('themeBtn');
      if (html.getAttribute('data-theme') === 'dark') {
        html.setAttribute('data-theme', 'light');
        localStorage.setItem('theme', 'light');
        btn.textContent = '\\u2600';
      } else {
        html.setAttribute('data-theme', 'dark');
        localStorage.setItem('theme', 'dark');
        btn.textContent = '\\u263E';
      }
    }
    (function() {
      var btn = document.getElementById('themeBtn');
      var theme = document.documentElement.getAttribute('data-theme');
      btn.textContent = theme === 'light' ? '\\u2600' : '\\u263E';
    })();
  </script>
  <table>
    <thead>
      <tr>
        <th>Name</th>
        <th class="right">Size</th>
        <th class="right">Modified</th>
      </tr>
    </thead>
    <tbody>
      ${parentRow}
      ${emptyRow || entryRows}
    </tbody>
  </table>
  <div class="footer">
    ${entries.length} item${entries.length !== 1 ? 's' : ''} &bull; ycy file server
  </div>
</body>
</html>`
}

// ─── Directory Listing ────────────────────────────────────────────────────────

async function serveDirectory(dirPath: string, urlPath: string): Promise<Response> {
  if (!urlPath.endsWith('/')) {
    return Response.redirect(`${urlPath}/`, 301)
  }

  let rawEntries: Dirent[]
  try {
    rawEntries = await fs.readdir(dirPath, { withFileTypes: true })
  }
  catch {
    return new Response('403 Forbidden', { status: 403, headers: { 'Content-Type': 'text/plain' } })
  }

  const entries: DirectoryEntry[] = []
  for (const dirent of rawEntries) {
    const fullPath = path.join(dirPath, dirent.name)
    let entryStat: Awaited<ReturnType<typeof fs.stat>>
    try {
      entryStat = await fs.stat(fullPath)
    }
    catch {
      continue
    }
    const isDir = entryStat.isDirectory()
    const encodedName = encodeURIComponent(dirent.name)
    const href = isDir ? `${urlPath}${encodedName}/` : `${urlPath}${encodedName}`
    entries.push({
      name: dirent.name,
      isDirectory: isDir,
      size: isDir ? 0 : entryStat.size,
      mtime: entryStat.mtime,
      href,
    })
  }

  entries.sort((a, b) => {
    if (a.isDirectory !== b.isDirectory)
      return a.isDirectory ? -1 : 1
    return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })
  })

  const html = buildDirectoryHtml(urlPath, entries)
  return new Response(html, { headers: { 'Content-Type': 'text/html; charset=utf-8' } })
}

// ─── CORS ─────────────────────────────────────────────────────────────────────

const CORS_HEADERS: Record<string, string> = {
  'Access-Control-Allow-Origin': '*',
  'Access-Control-Allow-Methods': 'GET, HEAD, OPTIONS',
  'Access-Control-Allow-Headers': '*',
  'Access-Control-Max-Age': '86400',
}

function withCors(res: Response): Response {
  for (const [key, value] of Object.entries(CORS_HEADERS)) {
    res.headers.set(key, value)
  }
  return res
}

// ─── Request Router ───────────────────────────────────────────────────────────

async function handleRequest(req: Request, root: string): Promise<Response> {
  if (req.method === 'OPTIONS') {
    return new Response(null, { status: 204, headers: CORS_HEADERS })
  }

  const url = new URL(req.url)
  const safePath = await resolveSafePath(root, url.pathname)

  if (safePath === null) {
    return withCors(new Response('403 Forbidden', { status: 403, headers: { 'Content-Type': 'text/plain' } }))
  }

  let stat: Awaited<ReturnType<typeof fs.stat>>
  try {
    stat = await fs.stat(safePath)
  }
  catch {
    return withCors(new Response('404 Not Found', { status: 404, headers: { 'Content-Type': 'text/plain' } }))
  }

  if (stat.isDirectory()) {
    return withCors(await serveDirectory(safePath, url.pathname))
  }

  return withCors(await serveFile(safePath, stat))
}

// ─── Main Export ──────────────────────────────────────────────────────────────

function getLanAddresses(): string[] {
  const interfaces = os.networkInterfaces()
  const addresses: string[] = []
  for (const nets of Object.values(interfaces)) {
    if (!nets)
      continue
    for (const net of nets) {
      if (net.family === 'IPv4' && !net.internal)
        addresses.push(net.address)
    }
  }
  return addresses
}

export async function serve(opt: ServeOptions): Promise<void> {
  printTitle()
  intro(ansis.bold('Static File Server'))

  const root = path.resolve(opt.directory)

  let rootStat: Awaited<ReturnType<typeof fs.stat>>
  try {
    rootStat = await fs.stat(root)
  }
  catch {
    cancel(`Directory not found: ${ansis.dim(root)}`)
    return
  }

  if (!rootStat.isDirectory()) {
    cancel(`Path is not a directory: ${ansis.dim(root)}`)
    return
  }

  let server: ReturnType<typeof Bun.serve>
  try {
    server = Bun.serve({
      port: opt.port,
      hostname: opt.address,
      fetch(req) {
        return handleRequest(req, root)
      },
    })
  }
  catch (err) {
    cancel(`Failed to start server: ${err instanceof Error ? err.message : String(err)}`)
    return
  }

  const displayAddress = opt.address === '0.0.0.0' ? 'localhost' : opt.address
  const url = `http://${displayAddress}:${server.port}`

  const msgs: string[] = []
  msgs.push(`  ${ansis.dim('Local')}     ${ansis.cyan(url)}`)

  if (opt.address === '0.0.0.0') {
    const lanAddrs = getLanAddresses()
    for (const addr of lanAddrs) {
      msgs.push(`  ${ansis.dim('Network')}   ${ansis.cyan(`http://${addr}:${server.port}`)}`)
    }
  }

  msgs.push(`  ${ansis.dim('Directory')} ${ansis.dim(root)}`)
  msgs.push(`  ${ansis.dim('Bind')}      ${ansis.dim(`${opt.address}:${server.port}`)}`)

  note(msgs.join('\n'), `Server running`)

  await new Promise<void>((resolve) => {
    const shutdown = (): void => {
      server.stop(true)
      outro('Server stopped.')
      resolve()
    }
    process.once('SIGINT', shutdown)
    process.once('SIGTERM', shutdown)
  })
}
