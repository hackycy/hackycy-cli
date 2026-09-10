const ARCHIVE_SUFFIXES = [
  '.tar.bzip2',
  '.tar.zstd',
  '.tar.bz2',
  '.tar.gz',
  '.tar.xz',
  '.tar.zst',
  '.tbz2',
  '.tzst',
  '.gzip',
  '.bzip2',
  '.zstd',
  '.tgz',
  '.tbz',
  '.txz',
  '.7z',
  '.zip',
  '.rar',
  '.tar',
  '.gz',
  '.bz2',
  '.xz',
  '.zst',
  '.cab',
  '.arj',
  '.lzh',
  '.lha',
  '.cpio',
] as const

const LAYERED_TAR_SUFFIXES = new Set([
  '.tar.bzip2',
  '.tar.zstd',
  '.tar.bz2',
  '.tar.gz',
  '.tar.xz',
  '.tar.zst',
  '.tbz2',
  '.tzst',
  '.tgz',
  '.tbz',
  '.txz',
])

export function archiveSuffix(filename: string): string | undefined {
  const lowercase = filename.toLowerCase()
  return ARCHIVE_SUFFIXES.find(suffix => lowercase.endsWith(suffix))
}

export function isExtractableArchiveName(filename: string): boolean {
  return archiveSuffix(filename) !== undefined
}

export function isLayeredTarArchiveName(filename: string): boolean {
  const suffix = archiveSuffix(filename)
  return suffix !== undefined && LAYERED_TAR_SUFFIXES.has(suffix)
}

export function archiveDestinationName(filename: string): string {
  const suffix = archiveSuffix(filename)
  if (!suffix)
    return 'Extracted'
  const name = filename.slice(0, -suffix.length).trim()
  return name && name !== '.' && name !== '..' ? name : 'Extracted'
}
