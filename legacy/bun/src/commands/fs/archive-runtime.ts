import { access, chmod, mkdir, rename, unlink, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { embeddedFiles } from 'bun'
import { SEVEN_ZIP_ARTIFACTS, SEVEN_ZIP_VERSION, sevenZipTarget } from './archive-manifest'
import { FsWorkspaceError } from './types'

function stateRoot(): string {
  if (process.platform === 'win32')
    return process.env.LOCALAPPDATA || path.join(os.homedir(), 'AppData', 'Local')
  if (process.platform === 'darwin')
    return path.join(os.homedir(), 'Library', 'Application Support')
  return process.env.XDG_STATE_HOME || path.join(os.homedir(), '.local', 'state')
}

function embeddedBlob(name: string): Blob | undefined {
  return embeddedFiles.find((file) => {
    const basename = path.basename((file as Blob & { name: string }).name)
    return basename === name
      || basename.startsWith(`${name}-`)
      || (basename.startsWith(`${path.parse(name).name}-`) && basename.endsWith(path.extname(name)))
  })
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

async function publishRuntimeFile(target: string, bytes: Uint8Array, metadata: { filename: string, executable?: boolean }): Promise<void> {
  const temporary = `${target}.candidate-${crypto.randomUUID()}`
  await writeFile(temporary, bytes, { mode: metadata.executable ? 0o755 : 0o644 })
  if (metadata.executable && process.platform !== 'win32')
    await chmod(temporary, 0o755)
  try {
    await unlink(target).catch(cause => (cause as NodeJS.ErrnoException).code === 'ENOENT' ? undefined : Promise.reject(cause))
    await rename(temporary, target)
  }
  catch (cause) {
    await unlink(temporary).catch(() => {})
    throw cause
  }
}

export async function ensureSevenZipRuntime(): Promise<string> {
  const key = sevenZipTarget(process.platform, process.arch)
  if (!key)
    throw new FsWorkspaceError('UNAVAILABLE', `7-Zip is not available for ${process.platform}-${process.arch}`)
  const files = SEVEN_ZIP_ARTIFACTS[key].files

  const embedded = files.map(file => ({ metadata: file, blob: embeddedBlob(file.embeddedName) }))
  if (embedded.some(item => item.blob) && !embedded.every(item => item.blob))
    throw new FsWorkspaceError('UNAVAILABLE', 'Embedded 7-Zip runtime is incomplete')
  if (embedded.every(item => item.blob)) {
    const directory = path.join(stateRoot(), 'ycy', '7zip', SEVEN_ZIP_VERSION)
    await mkdir(directory, { recursive: true })
    for (const item of embedded) {
      const target = path.join(directory, item.metadata.filename)
      if (!await fileExists(target))
        await publishRuntimeFile(target, new Uint8Array(await item.blob!.arrayBuffer()), item.metadata)
    }
    return path.join(directory, process.platform === 'win32' ? '7z.exe' : '7zz')
  }

  const systemRuntime = Bun.which('7zz') ?? Bun.which('7z')
  if (systemRuntime)
    return systemRuntime
  throw new FsWorkspaceError('UNAVAILABLE', '7-Zip runtime is unavailable; install 7zz or build with an embedded runtime')
}
