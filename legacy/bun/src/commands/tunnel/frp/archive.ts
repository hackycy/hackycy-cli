import { createHash } from 'node:crypto'
import { gunzipSync, unzipSync } from 'fflate'
import { parseTar } from '../../../shared/tar'
import { TunnelError } from '../types'

export type FrpPlatform = 'darwin' | 'linux' | 'win32'

export function sha256Bytes(bytes: Uint8Array): string {
  return createHash('sha256').update(bytes).digest('hex')
}

function executableName(role: 'frpc' | 'frps', platform: FrpPlatform): string {
  return platform === 'win32' ? `${role}.exe` : role
}

export function extractFrpBinaries(archive: Uint8Array, archiveName: string, platform: FrpPlatform): Record<'frpc' | 'frps', Uint8Array> {
  const files: Record<string, Uint8Array> = archiveName.endsWith('.zip')
    ? unzipSync(archive)
    : Object.fromEntries(parseTar(gunzipSync(archive)).filter(entry => entry.type === 'file').map(entry => [entry.name, entry.data]))
  const binaries = {} as Record<'frpc' | 'frps', Uint8Array>
  for (const role of ['frpc', 'frps'] as const) {
    const name = executableName(role, platform)
    const entry = Object.entries(files).find(([entryName]) => entryName.endsWith(`/${name}`))
    if (!entry)
      throw new TunnelError('INVALID_FRP_ARCHIVE', `${archiveName} does not contain ${name}`)
    binaries[role] = entry[1]
  }
  return binaries
}
