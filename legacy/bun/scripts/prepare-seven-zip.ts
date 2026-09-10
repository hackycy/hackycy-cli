import type { SevenZipArtifact, SevenZipTarget } from '../src/commands/fs/archive-manifest'
import { createHash } from 'node:crypto'
import { access, chmod, copyFile, mkdir, mkdtemp, readFile, rename, rm, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { SEVEN_ZIP_ARTIFACTS, SEVEN_ZIP_RELEASE_BASE_URL, sevenZipTarget } from '../src/commands/fs/archive-manifest'

const CACHE_ROOT = path.resolve('.tmp', '7zip')
const WINDOWS_EXTRACTOR = {
  asset: '7zr.exe',
  sha256: '56b8cc9f4971cef253644fafe54063ed7fdca551d4dee0f8c6baa81b855acd72',
}

function sha256(bytes: Uint8Array): string {
  return createHash('sha256').update(bytes).digest('hex')
}

async function validFile(filename: string, expected: string): Promise<boolean> {
  try {
    return sha256(new Uint8Array(await readFile(filename))) === expected
  }
  catch {
    return false
  }
}

async function fileExists(filename: string): Promise<boolean> {
  try {
    await access(filename)
    return true
  }
  catch {
    return false
  }
}

async function download(asset: string, expected: string): Promise<string> {
  const directory = path.join(CACHE_ROOT, 'downloads')
  const target = path.join(directory, asset)
  await mkdir(directory, { recursive: true })
  if (await validFile(target, expected))
    return target
  const response = await fetch(`${SEVEN_ZIP_RELEASE_BASE_URL}/${asset}`, { redirect: 'follow' })
  if (!response.ok)
    throw new Error(`Failed to download ${asset}: HTTP ${response.status}`)
  const bytes = new Uint8Array(await response.arrayBuffer())
  if (sha256(bytes) !== expected)
    throw new Error(`${asset} failed SHA-256 verification`)
  const temporary = `${target}.candidate-${crypto.randomUUID()}`
  await writeFile(temporary, bytes)
  await rename(temporary, target)
  return target
}

async function run(command: string[]): Promise<void> {
  const child = Bun.spawn(command, { stdin: 'ignore', stdout: 'ignore', stderr: 'pipe' })
  const errorOutput = new Response(child.stderr).text()
  const [exitCode, error] = await Promise.all([child.exited, errorOutput])
  if (exitCode !== 0)
    throw new Error(`${path.basename(command[0]!)} failed: ${error.trim() || `exit code ${exitCode}`}`)
}

async function windowsExtractor(): Promise<string> {
  const installed = Bun.which('7zz') ?? Bun.which('7z')
  if (installed)
    return installed
  if (process.platform !== 'win32')
    throw new Error('Preparing a Windows 7-Zip runtime on this host requires 7zz or 7z in PATH')
  return download(WINDOWS_EXTRACTOR.asset, WINDOWS_EXTRACTOR.sha256)
}

async function extractArtifact(artifact: SevenZipArtifact, archive: string, destination: string): Promise<void> {
  if (artifact.format === 'tar.xz') {
    await run(['tar', '-xJf', archive, '-C', destination])
    return
  }
  const extractor = await windowsExtractor()
  await run([extractor, 'x', '-y', `-o${destination}`, '--', archive])
}

export function sevenZipBuildTarget(compileTarget?: string): SevenZipTarget {
  if (!compileTarget) {
    const current = sevenZipTarget(process.platform, process.arch)
    if (current)
      return current
    throw new Error(`7-Zip does not support ${process.platform}-${process.arch}`)
  }
  const match = /^bun-(darwin|linux|windows)-(x64|arm64)(?:-.+)?$/.exec(compileTarget)
  if (!match)
    throw new Error(`7-Zip does not support build target ${compileTarget}`)
  const platform = match[1] === 'windows' ? 'win32' : match[1] as NodeJS.Platform
  const target = sevenZipTarget(platform, match[2]!)
  if (!target)
    throw new Error(`7-Zip does not support build target ${compileTarget}`)
  return target
}

export async function prepareSevenZipRuntime(compileTarget?: string): Promise<string[]> {
  const target = sevenZipBuildTarget(compileTarget)
  const artifact = SEVEN_ZIP_ARTIFACTS[target]
  const outputDirectory = path.join(CACHE_ROOT, 'runtime', target)
  const outputs = artifact.files.map(file => path.join(outputDirectory, file.embeddedName))
  if ((await Promise.all(outputs.map(fileExists))).every(Boolean))
    return outputs

  const archive = await download(artifact.asset, artifact.sha256)
  const temporary = await mkdtemp(path.join(os.tmpdir(), 'ycy-seven-zip-'))
  try {
    await extractArtifact(artifact, archive, temporary)
    await rm(outputDirectory, { recursive: true, force: true })
    await mkdir(outputDirectory, { recursive: true })
    for (const file of artifact.files) {
      const output = path.join(outputDirectory, file.embeddedName)
      await copyFile(path.join(temporary, file.sourceName), output)
      if (file.executable && process.platform !== 'win32')
        await chmod(output, 0o755)
    }
    return outputs
  }
  finally {
    await rm(temporary, { recursive: true, force: true })
  }
}

async function main(): Promise<void> {
  const targetIndex = process.argv.indexOf('--target')
  const target = targetIndex === -1 ? undefined : process.argv[targetIndex + 1]
  const outputs = await prepareSevenZipRuntime(target)
  for (const output of outputs)
    console.log(path.relative(process.cwd(), output))
}

if (import.meta.main) {
  await main().catch((cause) => {
    console.error(cause instanceof Error ? cause.message : cause)
    process.exit(1)
  })
}
