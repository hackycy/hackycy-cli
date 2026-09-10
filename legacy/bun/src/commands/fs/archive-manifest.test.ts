import { describe, expect, test } from 'bun:test'
import { SEVEN_ZIP_ARTIFACTS, sevenZipTarget } from './archive-manifest'

describe('7-Zip runtime manifest', () => {
  test('covers every released operating system and architecture pair', () => {
    expect(Object.keys(SEVEN_ZIP_ARTIFACTS).sort()).toEqual([
      'darwin-arm64',
      'darwin-x64',
      'linux-arm64',
      'linux-x64',
      'win32-arm64',
      'win32-x64',
    ])
    expect(sevenZipTarget('win32', 'x64')).toBe('win32-x64')
    expect(sevenZipTarget('freebsd', 'x64')).toBeUndefined()
  })

  test('pins archive digests and describes the embedded runtime files', () => {
    for (const [target, artifact] of Object.entries(SEVEN_ZIP_ARTIFACTS)) {
      expect(artifact.sha256).toMatch(/^[a-f\d]{64}$/)
      expect(artifact.files.some(file => file.executable)).toBe(true)
      expect(artifact.files.find(file => file.filename === 'License.txt')).toEqual(expect.objectContaining({
        sourceName: 'License.txt',
        embeddedName: 'ycy-7zip-License.bin',
      }))
      expect(new Set(artifact.files.map(file => file.embeddedName)).size).toBe(artifact.files.length)
      if (target.startsWith('win32-'))
        expect(artifact.files.map(file => file.filename)).toEqual(['7z.exe', '7z.dll', 'License.txt'])
      else
        expect(artifact.files.map(file => file.filename)).toEqual(['7zz', 'License.txt'])
    }
  })
})
