import { describe, expect, test } from 'bun:test'
import { archiveDestinationName, archiveSuffix, isExtractableArchiveName, isLayeredTarArchiveName } from './archive-support'

describe('archive support', () => {
  test('recognizes supported archive names case-insensitively', () => {
    for (const name of [
      'archive.7z',
      'archive.ZIP',
      'archive.rar',
      'archive.tar',
      'archive.gz',
      'archive.bzip2',
      'archive.xz',
      'archive.zstd',
      'archive.cab',
      'archive.arj',
      'archive.lzh',
      'archive.lha',
      'archive.cpio',
      'archive.tar.gz',
      'archive.tbz2',
      'archive.txz',
      'archive.tzst',
    ])
      expect(isExtractableArchiveName(name)).toBe(true)
    expect(isExtractableArchiveName('archive.iso')).toBe(false)
    expect(isExtractableArchiveName('archive.zip.txt')).toBe(false)
  })

  test('uses the longest suffix and preserves the complete base directory name', () => {
    expect(archiveSuffix('backup.tar.gz')).toBe('.tar.gz')
    expect(archiveDestinationName('backup.tar.gz')).toBe('backup')
    expect(archiveDestinationName('project.release.2026.tgz')).toBe('project.release.2026')
    expect(archiveDestinationName('.tar.gz')).toBe('Extracted')
    expect(isLayeredTarArchiveName('backup.TAR.ZST')).toBe(true)
    expect(isLayeredTarArchiveName('backup.gz')).toBe(false)
  })
})
