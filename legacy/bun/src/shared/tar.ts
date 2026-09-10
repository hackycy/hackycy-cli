/**
 * Minimal tar archive parser.
 * Handles POSIX ustar and GNU long name extensions.
 * Designed to work with fflate's gunzipSync for .tar.gz archives.
 */

const BLOCK_SIZE = 512
const NAME_OFFSET = 0
const NAME_LENGTH = 100
const SIZE_OFFSET = 124
const SIZE_LENGTH = 12
const TYPEFLAG_OFFSET = 156
const PREFIX_OFFSET = 345
const PREFIX_LENGTH = 155

export interface TarEntry {
  name: string
  size: number
  type: 'file' | 'directory' | 'other'
  data: Uint8Array
}

function decodeString(buf: Uint8Array, offset: number, length: number): string {
  let end = offset
  const max = offset + length
  while (end < max && buf[end] !== 0) end++
  return new TextDecoder().decode(buf.subarray(offset, end))
}

function parseOctal(buf: Uint8Array, offset: number, length: number): number {
  const str = decodeString(buf, offset, length).trim()
  if (!str)
    return 0
  return Number.parseInt(str, 8)
}

function isZeroBlock(buf: Uint8Array, offset: number): boolean {
  for (let i = offset; i < offset + BLOCK_SIZE; i++) {
    if (buf[i] !== 0)
      return false
  }
  return true
}

/**
 * Parse a tar archive (uncompressed) and return all entries.
 * Use with fflate's gunzipSync to handle .tar.gz:
 *
 * ```ts
 * import { gunzipSync } from 'fflate'
 * const tar = gunzipSync(gzData)
 * const entries = parseTar(tar)
 * ```
 */
export function parseTar(data: Uint8Array): TarEntry[] {
  const entries: TarEntry[] = []
  let offset = 0
  let gnuLongName: string | null = null

  while (offset + BLOCK_SIZE <= data.length) {
    if (isZeroBlock(data, offset)) {
      // Two consecutive zero blocks = end of archive
      if (offset + BLOCK_SIZE * 2 <= data.length && isZeroBlock(data, offset + BLOCK_SIZE))
        break
      offset += BLOCK_SIZE
      continue
    }

    const header = data.subarray(offset, offset + BLOCK_SIZE)
    const typeflag = String.fromCharCode(header[TYPEFLAG_OFFSET]!)

    // GNU long name extension: next entry's name is stored in this entry's data
    if (typeflag === 'L') {
      const size = parseOctal(header, SIZE_OFFSET, SIZE_LENGTH)
      offset += BLOCK_SIZE
      gnuLongName = new TextDecoder().decode(data.subarray(offset, offset + size)).replace(/\0+$/, '')
      offset += Math.ceil(size / BLOCK_SIZE) * BLOCK_SIZE
      continue
    }

    const prefix = decodeString(header, PREFIX_OFFSET, PREFIX_LENGTH)
    const rawName = decodeString(header, NAME_OFFSET, NAME_LENGTH)
    const name = gnuLongName ?? (prefix ? `${prefix}/${rawName}` : rawName)
    gnuLongName = null

    const size = parseOctal(header, SIZE_OFFSET, SIZE_LENGTH)

    let type: TarEntry['type']
    if (typeflag === '5' || name.endsWith('/'))
      type = 'directory'
    else if (typeflag === '0' || typeflag === '\0')
      type = 'file'
    else
      type = 'other'

    offset += BLOCK_SIZE
    const entryData = data.subarray(offset, offset + size)
    const paddedSize = Math.ceil(size / BLOCK_SIZE) * BLOCK_SIZE
    offset += paddedSize

    entries.push({ name, size, type, data: entryData })
  }

  return entries
}
