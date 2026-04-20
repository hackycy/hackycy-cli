import { mkdir, readdir, rm } from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import * as p from '@clack/prompts'
import ansis from 'ansis'
import { gunzipSync } from 'fflate'
import { parseTar } from '../../../shared/tar'
import { printTitle } from '../../../shared/utils'
import { getProvider } from './providers/base'
import { parseRepoUrl } from './url-parser'

export async function runGitFork(repoInput: string, dest?: string): Promise<void> {
  printTitle()
  p.intro(ansis.cyan('Git Fork'))

  // 1. Parse repo URL
  const s = p.spinner()
  s.start('Parsing repository URL...')

  let parsed: Awaited<ReturnType<typeof parseRepoUrl>>
  try {
    parsed = await parseRepoUrl(repoInput)
  }
  catch (err) {
    s.stop('Failed to parse URL')
    p.log.error((err as Error).message)
    process.exit(1)
  }

  const { host, scheme, owner, repo, providerType, token } = parsed
  const provider = getProvider(providerType)
  const baseUrl = `${scheme}://${host}`
  s.stop(`Resolved: ${ansis.dim(`${host}/${owner}/${repo}`)} ${ansis.dim(`(${providerType})`)}`)

  // 2. Determine destination
  const destDir = dest || repo
  const destPath = path.resolve(destDir)

  // Check if dest exists and is non-empty
  try {
    const entries = await readdir(destPath)
    if (entries.length > 0) {
      const shouldOverwrite = await p.confirm({
        message: `Directory "${destDir}" is not empty. Overwrite?`,
      })
      if (p.isCancel(shouldOverwrite) || !shouldOverwrite) {
        p.outro('Cancelled')
        process.exit(0)
      }
      await rm(destPath, { recursive: true, force: true })
    }
  }
  catch {
    // Directory doesn't exist, which is fine
  }

  // 3. Determine ref
  let ref = parsed.ref
  if (!ref) {
    s.start('Fetching default branch...')
    try {
      ref = await provider.getDefaultBranch(baseUrl, owner, repo, token)
      s.stop(`Branch: ${ansis.dim(ref)}`)
    }
    catch (err) {
      s.stop('Failed to get default branch')
      p.log.error((err as Error).message)
      process.exit(1)
    }
  }

  // 4. Try Archive API first
  let success = false
  s.start('Downloading archive...')

  try {
    const archiveUrl = provider.getArchiveUrl(baseUrl, owner, repo, ref)
    const headers = provider.buildArchiveHeaders(token)
    const res = await fetch(archiveUrl, { headers, redirect: 'follow' })

    if (!res.ok) {
      const statusText = res.status === 401 || res.status === 403
        ? 'Authentication failed. Check your token with "ycy git config add".'
        : `${res.status} ${res.statusText}`
      throw new Error(statusText)
    }

    const buffer = new Uint8Array(await res.arrayBuffer())
    const decompressed = gunzipSync(buffer)

    // Extract tar with strip-1 (remove top-level directory)
    await mkdir(destPath, { recursive: true })
    const entries = parseTar(decompressed)
    const writeOps: Promise<void>[] = []
    for (const entry of entries) {
      const slashIdx = entry.name.indexOf('/')
      if (slashIdx === -1)
        continue
      const stripped = entry.name.slice(slashIdx + 1)
      if (!stripped)
        continue
      const filePath = path.join(destPath, stripped)
      if (entry.type === 'directory') {
        writeOps.push(mkdir(filePath, { recursive: true }).then(() => {}))
      }
      else if (entry.type === 'file') {
        writeOps.push(
          mkdir(path.dirname(filePath), { recursive: true }).then(() =>
            Bun.write(filePath, entry.data).then(() => {}),
          ),
        )
      }
    }
    await Promise.all(writeOps)

    success = true
    s.stop('Archive downloaded and extracted')
  }
  catch (archiveErr) {
    s.stop(`Archive download failed: ${ansis.dim((archiveErr as Error).message)}`)
  }

  // 5. Fallback to git clone
  if (!success) {
    s.start('Falling back to git clone...')
    try {
      const cloneUrl = provider.buildCloneUrl(baseUrl, owner, repo, token)
      const proc = Bun.spawn(['git', 'clone', '--depth=1', '--single-branch', '--branch', ref, cloneUrl, destPath], {
        stdout: 'pipe',
        stderr: 'pipe',
      })
      const exitCode = await proc.exited
      if (exitCode !== 0) {
        const stderr = await new Response(proc.stderr).text()
        throw new Error(stderr.trim() || `git clone failed with exit code ${exitCode}`)
      }

      // Remove .git directory
      await rm(path.join(destPath, '.git'), { recursive: true, force: true })

      s.stop('Cloned and cleaned up')
    }
    catch (cloneErr) {
      s.stop('Clone failed')
      p.log.error((cloneErr as Error).message)
      process.exit(1)
    }
  }

  p.outro(`${ansis.green('Done!')} Project created at ${ansis.cyan(destDir)}`)
}
