import type { FrpArtifact } from './manifest'
import { randomUUID } from 'node:crypto'
import { chmod, mkdir, readFile, rename, rm, writeFile } from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import { managedFrpBinaryPath, managedFrpDirectory } from '../paths'
import { TunnelError } from '../types'
import { extractFrpBinaries, sha256Bytes } from './archive'
import { FRP_VERSION, resolveFrpArtifact } from './manifest'

export interface FrpBinaryOptions {
  env?: NodeJS.ProcessEnv
  platform?: NodeJS.Platform
  architecture?: NodeJS.Architecture
  fetch?: typeof globalThis.fetch
  signal?: AbortSignal
  verifyVersion?: (binaryPath: string) => Promise<void>
}

async function fileSha256(filePath: string): Promise<string | undefined> {
  try {
    return sha256Bytes(new Uint8Array(await readFile(filePath)))
  }
  catch (cause) {
    if ((cause as NodeJS.ErrnoException).code === 'ENOENT')
      return undefined
    throw cause
  }
}

function executableName(role: 'frpc' | 'frps', platform: string): string {
  return platform === 'win32' ? `${role}.exe` : role
}

export async function verifyFrpReportedVersion(binaryPath: string): Promise<void> {
  const child = Bun.spawn([binaryPath, '--version'], { stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const timeout = setTimeout(() => child.kill(), 5000)
  try {
    const [exitCode, output, errorOutput] = await Promise.all([
      child.exited,
      new Response(child.stdout).text(),
      new Response(child.stderr).text(),
    ])
    const reported = `${output}\n${errorOutput}`
    if (exitCode !== 0 || !new RegExp(`(?:^|\\D)v?${FRP_VERSION.replaceAll('.', '\\.')}(?:\\D|$)`).test(reported))
      throw new TunnelError('INVALID_FRP_VERSION', `${binaryPath} does not report FRP ${FRP_VERSION}`)
  }
  finally {
    clearTimeout(timeout)
  }
}

async function publishBinary(target: string, bytes: Uint8Array, expectedSha256: string): Promise<void> {
  if (sha256Bytes(bytes) !== expectedSha256)
    throw new TunnelError('INVALID_FRP_BINARY', `Extracted ${path.basename(target)} failed SHA-256 verification`)
  const temporary = `${target}.candidate-${randomUUID()}`
  const backup = `${target}.previous-${randomUUID()}`
  await writeFile(temporary, bytes, { mode: 0o755 })
  if (process.platform !== 'win32')
    await chmod(temporary, 0o755)
  let movedExisting = false
  try {
    try {
      await rename(target, backup)
      movedExisting = true
    }
    catch (cause) {
      if ((cause as NodeJS.ErrnoException).code !== 'ENOENT')
        throw cause
    }
    await rename(temporary, target)
    if (movedExisting)
      await rm(backup, { force: true })
  }
  catch (cause) {
    await rm(temporary, { force: true })
    if (movedExisting) {
      await rm(target, { force: true })
      await rename(backup, target)
    }
    throw cause
  }
}

export function manualFrpInstallMessage(role: 'frpc' | 'frps', target: string, artifact: FrpArtifact): string {
  const binarySha = role === 'frpc' ? artifact.frpcSha256 : artifact.frpsSha256
  return [
    `Could not install FRP ${artifact.version}.`,
    `Official archive: ${artifact.url}`,
    `Archive SHA-256: ${artifact.sha256}`,
    `Place ${executableName(role, artifact.platform)} at: ${target}`,
    `Binary SHA-256: ${binarySha}`,
  ].join('\n')
}

export async function ensureFrpBinary(role: 'frpc' | 'frps', options: FrpBinaryOptions = {}): Promise<string> {
  const env = options.env ?? process.env
  const platform = options.platform ?? process.platform
  const architecture = options.architecture ?? process.arch
  const artifact = resolveFrpArtifact(platform, architecture)
  const target = managedFrpBinaryPath(role, env, platform)
  const expected = role === 'frpc' ? artifact.frpcSha256 : artifact.frpsSha256
  const verifyVersion = options.verifyVersion ?? verifyFrpReportedVersion
  if (await fileSha256(target) === expected) {
    await verifyVersion(target)
    return target
  }

  try {
    const response = await (options.fetch ?? globalThis.fetch)(artifact.url, { redirect: 'follow', signal: options.signal })
    if (!response.ok)
      throw new Error(`download returned HTTP ${response.status}`)
    const archive = new Uint8Array(await response.arrayBuffer())
    if (sha256Bytes(archive) !== artifact.sha256)
      throw new TunnelError('INVALID_FRP_ARCHIVE', `${artifact.archive} failed SHA-256 verification`)
    const binaries = extractFrpBinaries(archive, artifact.archive, artifact.platform)
    await mkdir(managedFrpDirectory(env, platform), { recursive: true })
    await publishBinary(managedFrpBinaryPath('frpc', env, platform), binaries.frpc, artifact.frpcSha256)
    await publishBinary(managedFrpBinaryPath('frps', env, platform), binaries.frps, artifact.frpsSha256)
    await verifyVersion(target)
    return target
  }
  catch (cause) {
    throw new TunnelError('FRP_INSTALL_FAILED', `${manualFrpInstallMessage(role, target, artifact)}\nReason: ${cause instanceof Error ? cause.message : String(cause)}`)
  }
}
