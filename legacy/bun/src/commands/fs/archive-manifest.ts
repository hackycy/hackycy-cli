export const SEVEN_ZIP_VERSION = '26.02'
export const SEVEN_ZIP_RELEASE_BASE_URL = `https://github.com/ip7z/7zip/releases/download/${SEVEN_ZIP_VERSION}`

export type SevenZipTarget = 'darwin-arm64' | 'darwin-x64' | 'linux-arm64' | 'linux-x64' | 'win32-arm64' | 'win32-x64'

export interface SevenZipRuntimeFile {
  sourceName: string
  embeddedName: string
  filename: string
  executable?: boolean
}

export interface SevenZipArtifact {
  asset: string
  sha256: string
  format: 'tar.xz' | 'windows-installer'
  files: SevenZipRuntimeFile[]
}

const UNIX_LICENSE: SevenZipRuntimeFile = {
  sourceName: 'License.txt',
  embeddedName: 'ycy-7zip-License.bin',
  filename: 'License.txt',
}

const WINDOWS_LICENSE: SevenZipRuntimeFile = {
  sourceName: 'License.txt',
  embeddedName: 'ycy-7zip-License.bin',
  filename: 'License.txt',
}

export const SEVEN_ZIP_ARTIFACTS: Record<SevenZipTarget, SevenZipArtifact> = {
  'darwin-arm64': {
    asset: '7z2602-mac.tar.xz',
    sha256: '1cf6760579502f87e591ff5c73a005ec50b3e4d6f507e8b038382d563c3175b9',
    format: 'tar.xz',
    files: [
      { sourceName: '7zz', embeddedName: 'ycy-7zz.bin', filename: '7zz', executable: true },
      UNIX_LICENSE,
    ],
  },
  'darwin-x64': {
    asset: '7z2602-mac.tar.xz',
    sha256: '1cf6760579502f87e591ff5c73a005ec50b3e4d6f507e8b038382d563c3175b9',
    format: 'tar.xz',
    files: [
      { sourceName: '7zz', embeddedName: 'ycy-7zz.bin', filename: '7zz', executable: true },
      UNIX_LICENSE,
    ],
  },
  'linux-arm64': {
    asset: '7z2602-linux-arm64.tar.xz',
    sha256: '70ea6cc737ae1495ea2d7eb20ef3120fe579bd3f1a83a9d2362b62ec5bde2bba',
    format: 'tar.xz',
    files: [
      { sourceName: '7zz', embeddedName: 'ycy-7zz.bin', filename: '7zz', executable: true },
      UNIX_LICENSE,
    ],
  },
  'linux-x64': {
    asset: '7z2602-linux-x64.tar.xz',
    sha256: '41aaba7b1235304ab5aa0624530c67ae829496cd29e875925271efdccc28c03e',
    format: 'tar.xz',
    files: [
      { sourceName: '7zz', embeddedName: 'ycy-7zz.bin', filename: '7zz', executable: true },
      UNIX_LICENSE,
    ],
  },
  'win32-arm64': {
    asset: '7z2602-arm64.exe',
    sha256: '7c6fde79ed5e11b81c7bb6573b7962d3b6322aa5fce69c33ed19f672b55173ab',
    format: 'windows-installer',
    files: [
      { sourceName: '7z.exe', embeddedName: 'ycy-7z.exe', filename: '7z.exe', executable: true },
      { sourceName: '7z.dll', embeddedName: 'ycy-7z.dll', filename: '7z.dll' },
      WINDOWS_LICENSE,
    ],
  },
  'win32-x64': {
    asset: '7z2602-x64.exe',
    sha256: '6745fa76dc2ea031596d8678f6f6b99c3c1b435b4164a63485adbbc7b8d82ef0',
    format: 'windows-installer',
    files: [
      { sourceName: '7z.exe', embeddedName: 'ycy-7z.exe', filename: '7z.exe', executable: true },
      { sourceName: '7z.dll', embeddedName: 'ycy-7z.dll', filename: '7z.dll' },
      WINDOWS_LICENSE,
    ],
  },
}

export function sevenZipTarget(platform: NodeJS.Platform, architecture: string): SevenZipTarget | undefined {
  const candidate = `${platform}-${architecture}`
  return candidate in SEVEN_ZIP_ARTIFACTS ? candidate as SevenZipTarget : undefined
}
