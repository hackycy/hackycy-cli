import type { Stats, StatsFs } from 'node:fs'
import type { ArchiveExtractor } from './archive-extractor'
import type {
  FsChunkedUpload,
  FsDirectoryEntry,
  FsDirectoryListing,
  FsFile,
  FsOperation,
  FsOperationItem,
  FsPreviewKind,
  FsStreamWriteOptions,
  FsTextPreview,
  FsTextSaveResult,
  FsUploadResult,
  FsWorkspace,
} from './types'
import { createHash } from 'node:crypto'
import fs from 'node:fs/promises'
import path from 'node:path'
import { getFiletypeFromFileName } from '@pierre/diffs'
import { createSevenZipArchiveExtractor } from './archive-extractor'
import { archiveDestinationName, isExtractableArchiveName } from './archive-support'
import { FsWorkspaceError } from './types'

const IMAGE_MIME_TYPES = new Map([
  ['.avif', 'image/avif'],
  ['.gif', 'image/gif'],
  ['.jpeg', 'image/jpeg'],
  ['.jpg', 'image/jpeg'],
  ['.png', 'image/png'],
  ['.svg', 'image/svg+xml'],
  ['.webp', 'image/webp'],
])

const THUMBNAIL_EXTENSIONS = new Set(['.avif', '.gif', '.jpeg', '.jpg', '.png', '.webp'])
const DOWNLOAD_TEMPORARY_NAME = /^\.download-[0-9a-f-]{36}\.tmp$/i
const EXTRACTION_TEMPORARY_NAME = /^\.extract-[0-9a-f-]{36}\.tmp(?:\.outer)?$/i
const EDIT_TEMPORARY_NAME = /^\.edit-[0-9a-f-]{36}\.tmp$/i
const UPLOAD_TEMPORARY_NAME = /^\.upload-[0-9a-f-]{36}\.tmp$/i
const EXTRACTION_TEMPORARY_MAX_AGE_MS = 24 * 60 * 60 * 1000
const UPLOAD_TEMPORARY_MAX_AGE_MS = 24 * 60 * 60 * 1000
const MAX_RESERVED_UPLOAD_BYTES = 1024 ** 3

export const MAX_TEXT_PREVIEW_BYTES = 10 * 1024 * 1024
export const MAX_UPLOAD_BYTES = 1024 * 1024 * 1024

function normalizedRelativePath(value: string): string {
  if (value.includes('\0') || value.includes('\\') || value.startsWith('/') || /^[a-z]:/i.test(value))
    throw new FsWorkspaceError('INVALID_PATH', 'Path must be relative to the file browser directory')

  const segments = value.split('/')
  if (segments.some(segment => segment === '.' || segment === '..'))
    throw new FsWorkspaceError('PATH_FORBIDDEN', 'Path escapes the file browser directory')
  return segments.filter(Boolean).join('/')
}

function isWithinRoot(root: string, candidate: string): boolean {
  const rootWithSeparator = root.endsWith(path.sep) ? root : `${root}${path.sep}`
  return candidate === root || candidate.startsWith(rootWithSeparator)
}

function absolutePath(root: string, relativePath: string): string {
  return path.join(root, ...relativePath.split('/').filter(Boolean))
}

function encodedPath(relativePath: string): string {
  return relativePath.split('/').map(encodeURIComponent).join('/')
}

function uploadFilename(value: string): string {
  const filename = value.trim()
  if (!filename || filename === '.' || filename === '..' || filename.includes('/') || filename.includes('\\') || filename.includes('\0'))
    throw new FsWorkspaceError('INVALID_UPLOAD', 'Upload filename is invalid')
  return filename
}

function operationName(value: string): string {
  if (!value.trim() || value === '.' || value === '..' || value.includes('/') || value.includes('\\') || value.includes('\0'))
    throw new FsWorkspaceError('INVALID_NAME', 'Entry name is invalid')
  return value
}

function operationFailure(cause: unknown, paths: { sourcePath?: string, destinationPath?: string }): FsOperationItem {
  const error = cause instanceof FsWorkspaceError
    ? cause
    : new FsWorkspaceError('UNAVAILABLE', 'Filesystem operation failed')
  return {
    status: 'error',
    ...paths,
    error: { code: error.code, message: error.message },
  }
}

async function operationDirectory(root: string, requestedPath: string): Promise<{ relativePath: string, resolved: string }> {
  const relativePath = normalizedRelativePath(requestedPath)
  const resolved = await resolvedInsideRoot(root, absolutePath(root, relativePath))
  if (!(await fs.stat(resolved)).isDirectory())
    throw new FsWorkspaceError('NOT_DIRECTORY', 'Operation target is not a directory')
  return { relativePath, resolved }
}

async function operationEntry(root: string, requestedPath: string): Promise<{ relativePath: string, name: string, resolved: string }> {
  const relativePath = normalizedRelativePath(requestedPath)
  if (!relativePath)
    throw new FsWorkspaceError('ROOT_IMMUTABLE', 'The file browser root cannot be changed')
  const segments = relativePath.split('/')
  const name = segments.pop()!
  const parent = await operationDirectory(root, segments.join('/'))
  const resolved = path.join(parent.resolved, name)
  try {
    await fs.lstat(resolved)
  }
  catch {
    throw new FsWorkspaceError('NOT_FOUND', 'Path does not exist')
  }
  return { relativePath, name, resolved }
}

async function requireMissing(candidate: string): Promise<void> {
  try {
    await fs.lstat(candidate)
  }
  catch (cause) {
    if ((cause as NodeJS.ErrnoException).code === 'ENOENT')
      return
    throw cause
  }
  throw new FsWorkspaceError('ALREADY_EXISTS', 'An entry with that name already exists')
}

async function collisionDestination(directory: string, filename: string): Promise<{ filename: string, resolved: string }> {
  for (let index = 0; index <= 9999; index++) {
    const candidateName = collisionFilename(filename, index)
    const resolved = path.join(directory, candidateName)
    try {
      await fs.lstat(resolved)
    }
    catch (cause) {
      if ((cause as NodeJS.ErrnoException).code === 'ENOENT')
        return { filename: candidateName, resolved }
      throw cause
    }
  }
  throw new FsWorkspaceError('NAME_EXHAUSTED', 'Too many entries have the same name')
}

async function collisionDirectoryDestination(directory: string, name: string): Promise<{ name: string, resolved: string }> {
  for (let index = 0; index <= 9999; index++) {
    const candidateName = index === 0 ? name : `${name} (${index})`
    const resolved = path.join(directory, candidateName)
    try {
      await fs.lstat(resolved)
    }
    catch (cause) {
      if ((cause as NodeJS.ErrnoException).code === 'ENOENT')
        return { name: candidateName, resolved }
      throw cause
    }
  }
  throw new FsWorkspaceError('NAME_EXHAUSTED', 'Too many directories have the same name')
}

function collisionFilename(filename: string, index: number): string {
  if (index === 0)
    return filename
  const lastDot = filename.lastIndexOf('.')
  const base = lastDot > 0 ? filename.slice(0, lastDot) : filename
  const extension = lastDot > 0 ? filename.slice(lastDot) : ''
  return `${base} (${index})${extension}`
}

async function publishTemporaryFile(
  directory: string,
  directoryPath: string,
  filename: string,
  temporaryPath: string,
  size: number,
): Promise<FsUploadResult> {
  for (let index = 0; index <= 9999; index++) {
    const finalFilename = collisionFilename(filename, index)
    const finalPath = path.join(directory, finalFilename)
    try {
      await fs.link(temporaryPath, finalPath)
      await fs.unlink(temporaryPath)
      return {
        filename: finalFilename,
        path: [directoryPath, finalFilename].filter(Boolean).join('/'),
        size,
      }
    }
    catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'EEXIST')
        throw error
    }
  }
  throw new FsWorkspaceError('NAME_EXHAUSTED', 'Too many files have the same name')
}

function syntaxLanguage(filename: string): string | undefined {
  if (/^\.env(?:\..+)?$/i.test(filename))
    return 'dotenv'

  const exact = getFiletypeFromFileName(filename)
  if (exact !== 'text')
    return exact
  const lowercase = getFiletypeFromFileName(filename.toLowerCase())
  return lowercase === 'text' ? undefined : lowercase
}

function previewKind(mimeType: string, language: string | undefined): FsPreviewKind {
  const baseMimeType = mimeType.split(';')[0]!.trim().toLowerCase()
  if (baseMimeType.startsWith('image/'))
    return 'image'
  if (language)
    return 'text'
  if (baseMimeType.startsWith('video/'))
    return 'video'
  if (baseMimeType.startsWith('audio/'))
    return 'audio'
  if (baseMimeType === 'application/pdf')
    return 'pdf'
  if (baseMimeType.startsWith('text/') || [
    'application/json',
    'application/javascript',
    'application/ld+json',
    'application/xml',
    'application/xhtml+xml',
  ].includes(baseMimeType)) {
    return 'text'
  }
  return 'none'
}

interface DecodedText {
  text: string
  encoding: 'utf-8' | 'utf-16le' | 'utf-16be'
  bom: boolean
}

function decodeText(bytes: Uint8Array): DecodedText | undefined {
  try {
    if (bytes[0] === 0xFF && bytes[1] === 0xFE)
      return { text: new TextDecoder('utf-16le', { fatal: true }).decode(bytes), encoding: 'utf-16le', bom: true }
    if (bytes[0] === 0xFE && bytes[1] === 0xFF)
      return { text: new TextDecoder('utf-16be', { fatal: true }).decode(bytes), encoding: 'utf-16be', bom: true }
    const bom = bytes[0] === 0xEF && bytes[1] === 0xBB && bytes[2] === 0xBF
    return { text: new TextDecoder('utf-8', { fatal: true }).decode(bytes), encoding: 'utf-8', bom }
  }
  catch {
    return undefined
  }
}

function revision(bytes: Uint8Array): string {
  return createHash('sha256').update(bytes).digest('hex')
}

function lineEndingStyle(text: string): '\n' | '\r\n' | '\r' {
  let crlf = 0
  let lf = 0
  let cr = 0
  for (let index = 0; index < text.length; index++) {
    if (text[index] === '\r') {
      if (text[index + 1] === '\n') {
        crlf++
        index++
      }
      else {
        cr++
      }
    }
    else if (text[index] === '\n') {
      lf++
    }
  }
  if (crlf > lf && crlf > cr)
    return '\r\n'
  if (cr > lf && cr > crlf)
    return '\r'
  return '\n'
}

function normalizeDraft(text: string, source: string): string {
  const endedWithNewline = /(?:\r\n|\r|\n)$/.test(source)
  const normalized = text.replace(/\r\n|\r|\n/g, '\n')
  const withoutTrailing = normalized.replace(/\n+$/g, '')
  const preserved = endedWithNewline ? `${withoutTrailing}\n` : withoutTrailing
  const separator = lineEndingStyle(source)
  return separator === '\n' ? preserved : preserved.replaceAll('\n', separator)
}

function encodeText(text: string, encoding: DecodedText['encoding'], bom: boolean): Uint8Array {
  if (encoding === 'utf-8') {
    const encoded = new TextEncoder().encode(text)
    if (!bom)
      return encoded
    const result = new Uint8Array(encoded.byteLength + 3)
    result.set([0xEF, 0xBB, 0xBF])
    result.set(encoded, 3)
    return result
  }
  const result = new Uint8Array(text.length * 2 + 2)
  result[0] = encoding === 'utf-16le' ? 0xFF : 0xFE
  result[1] = encoding === 'utf-16le' ? 0xFE : 0xFF
  for (let index = 0; index < text.length; index++) {
    const code = text.charCodeAt(index)
    if (encoding === 'utf-16le') {
      result[2 + index * 2] = code & 0xFF
      result[3 + index * 2] = code >>> 8
    }
    else {
      result[2 + index * 2] = code >>> 8
      result[3 + index * 2] = code & 0xFF
    }
  }
  return result
}

async function readTextBytes(resolved: string): Promise<{ bytes: Uint8Array, size: number, stat: Stats }> {
  const stat = await fs.stat(resolved)
  if (!stat.isFile())
    throw new FsWorkspaceError('NOT_FILE', 'Path is not a file')
  if (stat.size > MAX_TEXT_PREVIEW_BYTES)
    return { bytes: new Uint8Array(), size: stat.size, stat }
  const handle = await fs.open(resolved, 'r')
  try {
    const buffer = new Uint8Array(MAX_TEXT_PREVIEW_BYTES)
    let bytesRead = 0
    while (bytesRead < buffer.length) {
      const result = await handle.read(buffer, bytesRead, buffer.length - bytesRead, bytesRead)
      if (result.bytesRead === 0)
        break
      bytesRead += result.bytesRead
    }
    const currentStat = await handle.stat()
    if (currentStat.size > MAX_TEXT_PREVIEW_BYTES)
      return { bytes: new Uint8Array(), size: currentStat.size, stat: currentStat }
    return { bytes: buffer.subarray(0, bytesRead), size: currentStat.size, stat: currentStat }
  }
  finally {
    await handle.close()
  }
}

const saveLocks = new Map<string, Promise<void>>()

async function withSaveLock<T>(key: string, operation: () => Promise<T>): Promise<T> {
  const previous = saveLocks.get(key) ?? Promise.resolve()
  let release!: () => void
  const current = new Promise<void>(resolve => release = resolve)
  saveLocks.set(key, current)
  await previous
  try {
    return await operation()
  }
  finally {
    release()
    if (saveLocks.get(key) === current)
      saveLocks.delete(key)
  }
}

async function resolvedInsideRoot(root: string, candidate: string): Promise<string> {
  let resolved: string
  try {
    resolved = await fs.realpath(candidate)
  }
  catch {
    throw new FsWorkspaceError('NOT_FOUND', 'Path does not exist')
  }
  if (!isWithinRoot(root, resolved))
    throw new FsWorkspaceError('PATH_FORBIDDEN', 'Path escapes the file browser directory')
  return resolved
}

async function browserEntry(root: string, directoryPath: string, name: string): Promise<FsDirectoryEntry> {
  const relativePath = [directoryPath, name].filter(Boolean).join('/')
  const candidate = absolutePath(root, relativePath)
  let linkStat: Stats
  try {
    linkStat = await fs.lstat(candidate)
  }
  catch {
    return { name, path: relativePath, kind: 'unavailable', isSymlink: false, previewKind: 'none', extractable: false }
  }

  const isSymlink = linkStat.isSymbolicLink()
  let resolved: string
  let stat: Stats
  try {
    resolved = await resolvedInsideRoot(root, candidate)
    stat = await fs.stat(resolved)
  }
  catch {
    return {
      name,
      path: relativePath,
      kind: 'unavailable',
      isSymlink,
      modifiedAt: linkStat.mtime.toISOString(),
      previewKind: 'none',
      extractable: false,
    }
  }

  if (stat.isDirectory()) {
    return {
      name,
      path: relativePath,
      kind: 'directory',
      isSymlink,
      modifiedAt: stat.mtime.toISOString(),
      previewKind: 'none',
      browseUrl: `/browse/${encodedPath(relativePath)}`,
      extractable: false,
    }
  }

  if (!stat.isFile()) {
    return {
      name,
      path: relativePath,
      kind: 'unavailable',
      isSymlink,
      modifiedAt: stat.mtime.toISOString(),
      previewKind: 'none',
      extractable: false,
    }
  }

  const extensionMimeType = IMAGE_MIME_TYPES.get(path.extname(name).toLowerCase())
  const mimeType = extensionMimeType ?? Bun.file(resolved).type ?? 'application/octet-stream'
  const language = syntaxLanguage(name)
  const urlPath = `/files/${encodedPath(relativePath)}`
  const thumbnailUrl = THUMBNAIL_EXTENSIONS.has(path.extname(name).toLowerCase())
    ? `/thumbnails/${encodedPath(relativePath)}`
    : undefined
  return {
    name,
    path: relativePath,
    kind: 'file',
    isSymlink,
    size: stat.size,
    modifiedAt: stat.mtime.toISOString(),
    mimeType,
    previewKind: previewKind(mimeType, language),
    syntaxLanguage: language,
    fileUrl: urlPath,
    thumbnailUrl,
    downloadUrl: `${urlPath}?download=1`,
    extractable: isExtractableArchiveName(name),
  }
}

async function cleanupStaleExtractionDirectories(directory: string): Promise<void> {
  let entries: string[]
  try {
    entries = await fs.readdir(directory)
  }
  catch {
    return
  }
  const threshold = Date.now() - EXTRACTION_TEMPORARY_MAX_AGE_MS
  await Promise.all(entries.filter(name => EXTRACTION_TEMPORARY_NAME.test(name)).map(async (name) => {
    const target = path.join(directory, name)
    try {
      const stat = await fs.lstat(target)
      if (stat.isDirectory() && stat.mtimeMs < threshold)
        await fs.rm(target, { recursive: true, force: true })
    }
    catch {}
  }))
}

async function cleanupStaleUploadFiles(directory: string): Promise<void> {
  let entries: string[]
  try {
    entries = await fs.readdir(directory)
  }
  catch {
    return
  }
  const threshold = Date.now() - UPLOAD_TEMPORARY_MAX_AGE_MS
  await Promise.all(entries.filter(name => UPLOAD_TEMPORARY_NAME.test(name)).map(async (name) => {
    const target = path.join(directory, name)
    try {
      const stat = await fs.lstat(target)
      if (stat.isFile() && stat.mtimeMs < threshold)
        await fs.unlink(target)
    }
    catch {}
  }))
}

async function validateExtractedTree(root: string): Promise<void> {
  const walk = async (directory: string): Promise<void> => {
    for (const name of await fs.readdir(directory)) {
      const candidate = path.join(directory, name)
      const stat = await fs.lstat(candidate)
      if (stat.isDirectory()) {
        await walk(candidate)
        continue
      }
      if (stat.isFile())
        continue
      if (!stat.isSymbolicLink())
        throw new FsWorkspaceError('INVALID_ARCHIVE', 'Archive contains an unsupported special filesystem entry')
      const target = await fs.readlink(candidate)
      if (path.isAbsolute(target))
        throw new FsWorkspaceError('INVALID_ARCHIVE', 'Archive contains an unsafe symbolic link')
      const resolved = path.resolve(path.dirname(candidate), target)
      if (!isWithinRoot(root, resolved))
        throw new FsWorkspaceError('INVALID_ARCHIVE', 'Archive contains a symbolic link that escapes its destination')
      try {
        if (!isWithinRoot(root, await fs.realpath(candidate)))
          throw new FsWorkspaceError('INVALID_ARCHIVE', 'Archive contains a symbolic link that escapes its destination')
      }
      catch (cause) {
        if (cause instanceof FsWorkspaceError)
          throw cause
        throw new FsWorkspaceError('INVALID_ARCHIVE', 'Archive contains a broken symbolic link')
      }
    }
  }
  await walk(root)
}

export interface FsWorkspaceOptions {
  archiveExtractor?: ArchiveExtractor
  statfs?: (target: string) => Promise<StatsFs>
}

export async function createFsWorkspace(directory: string, options: FsWorkspaceOptions = {}): Promise<FsWorkspace> {
  let root: string
  try {
    root = await fs.realpath(path.resolve(directory))
  }
  catch {
    throw new FsWorkspaceError('NOT_FOUND', `Directory not found: ${path.resolve(directory)}`)
  }
  const rootStat = await fs.stat(root)
  if (!rootStat.isDirectory())
    throw new FsWorkspaceError('NOT_DIRECTORY', `Path is not a directory: ${root}`)
  const archiveExtractor = options.archiveExtractor ?? createSevenZipArchiveExtractor()
  const statfs = options.statfs ?? (target => fs.statfs(target))
  const reservedUploadBytes = new Map<string, number>()
  let reservationLock = Promise.resolve()
  const withReservationLock = async <T>(operation: () => Promise<T>): Promise<T> => {
    const previous = reservationLock
    let release!: () => void
    reservationLock = new Promise<void>((resolve) => {
      release = resolve
    })
    await previous
    try {
      return await operation()
    }
    finally {
      release()
    }
  }

  return {
    async listDirectory(requestedPath): Promise<FsDirectoryListing> {
      const relativePath = normalizedRelativePath(requestedPath)
      const resolved = await resolvedInsideRoot(root, absolutePath(root, relativePath))
      const stat = await fs.stat(resolved)
      if (!stat.isDirectory())
        throw new FsWorkspaceError('NOT_DIRECTORY', 'Path is not a directory')

      let names: string[]
      try {
        names = (await fs.readdir(resolved)).filter(name => !DOWNLOAD_TEMPORARY_NAME.test(name) && !EXTRACTION_TEMPORARY_NAME.test(name) && !EDIT_TEMPORARY_NAME.test(name) && !UPLOAD_TEMPORARY_NAME.test(name))
      }
      catch {
        throw new FsWorkspaceError('UNAVAILABLE', 'Directory cannot be read')
      }
      const entries = await Promise.all(names.map(name => browserEntry(root, relativePath, name)))
      entries.sort((left, right) => {
        const leftRank = left.kind === 'directory' ? 0 : left.kind === 'file' ? 1 : 2
        const rightRank = right.kind === 'directory' ? 0 : right.kind === 'file' ? 1 : 2
        return leftRank - rightRank || left.name.localeCompare(right.name, undefined, { sensitivity: 'base' }) || left.name.localeCompare(right.name)
      })

      const parentSegments = relativePath.split('/').filter(Boolean)
      parentSegments.pop()
      return {
        rootName: path.basename(root) || root,
        path: relativePath,
        parentPath: relativePath ? parentSegments.join('/') : undefined,
        entries,
      }
    },
    async openFile(requestedPath): Promise<FsFile> {
      const relativePath = normalizedRelativePath(requestedPath)
      const resolved = await resolvedInsideRoot(root, absolutePath(root, relativePath))
      const stat = await fs.stat(resolved)
      if (!stat.isFile())
        throw new FsWorkspaceError('NOT_FILE', 'Path is not a file')
      const file = Bun.file(resolved)
      return {
        name: path.basename(relativePath),
        size: stat.size,
        modifiedAt: stat.mtime,
        mimeType: IMAGE_MIME_TYPES.get(path.extname(relativePath).toLowerCase()) ?? file.type ?? 'application/octet-stream',
        body: file,
      }
    },
    async readTextPreview(requestedPath): Promise<FsTextPreview> {
      const relativePath = normalizedRelativePath(requestedPath)
      const resolved = await resolvedInsideRoot(root, absolutePath(root, relativePath))
      const { bytes, size } = await readTextBytes(resolved)
      if (size > MAX_TEXT_PREVIEW_BYTES) {
        return {
          status: 'too_large',
          size,
          maxBytes: MAX_TEXT_PREVIEW_BYTES,
        }
      }
      const decoded = decodeText(bytes)
      return decoded
        ? { status: 'ready', text: decoded.text, encoding: decoded.encoding, size, revision: revision(bytes) }
        : { status: 'binary', size }
    },
    async saveTextFile(requestedPath, text, expectedRevision): Promise<FsTextSaveResult> {
      const relativePath = normalizedRelativePath(requestedPath)
      const candidate = absolutePath(root, relativePath)
      const resolved = await resolvedInsideRoot(root, candidate)
      return withSaveLock(resolved, async () => {
        let linkStat: Stats
        try {
          linkStat = await fs.lstat(candidate)
        }
        catch {
          throw new FsWorkspaceError('NOT_FOUND', 'Path does not exist')
        }
        if (linkStat.isSymbolicLink())
          throw new FsWorkspaceError('NOT_FILE', 'Symbolic links are not edit targets')
        if (!linkStat.isFile())
          throw new FsWorkspaceError('NOT_FILE', 'Path is not a file')

        const source = await readTextBytes(resolved)
        if (source.size > MAX_TEXT_PREVIEW_BYTES)
          throw new FsWorkspaceError('TOO_LARGE', 'Text file exceeds the 10 MiB limit')
        const decoded = decodeText(source.bytes)
        if (!decoded)
          throw new FsWorkspaceError('UNSUPPORTED_TEXT', 'File contents are not supported text')
        const currentRevision = revision(source.bytes)
        if (currentRevision !== expectedRevision)
          throw new FsWorkspaceError('REVISION_MISMATCH', 'The file changed while it was being edited')

        const normalized = normalizeDraft(text, decoded.text)
        const output = encodeText(normalized, decoded.encoding, decoded.bom)
        if (output.byteLength > MAX_TEXT_PREVIEW_BYTES)
          throw new FsWorkspaceError('TOO_LARGE', 'Edited text exceeds the 10 MiB limit')

        const temporaryPath = path.join(path.dirname(candidate), `.edit-${crypto.randomUUID()}.tmp`)
        try {
          const handle = await fs.open(temporaryPath, 'wx', linkStat.mode & 0o7777)
          try {
            await handle.writeFile(output)
            await handle.chmod(linkStat.mode & 0o7777)
            await handle.sync()
          }
          finally {
            await handle.close()
          }

          const finalLinkStat = await fs.lstat(candidate)
          if (!finalLinkStat.isFile() || finalLinkStat.isSymbolicLink())
            throw new FsWorkspaceError('NOT_FILE', 'Symbolic links are not edit targets')
          if (await fs.realpath(candidate) !== resolved)
            throw new FsWorkspaceError('REVISION_MISMATCH', 'The file changed while it was being edited')
          const finalSource = await readTextBytes(resolved)
          if (finalSource.size > MAX_TEXT_PREVIEW_BYTES || revision(finalSource.bytes) !== expectedRevision)
            throw new FsWorkspaceError('REVISION_MISMATCH', 'The file changed while it was being edited')
          await fs.rename(temporaryPath, candidate)
          const finalStat = await fs.stat(candidate)
          return {
            revision: revision(output),
            size: output.byteLength,
            modifiedAt: finalStat.mtime,
            encoding: decoded.encoding,
          }
        }
        catch (cause) {
          await fs.unlink(temporaryPath).catch(() => {})
          throw cause
        }
      })
    },
    async uploadFile(requestedDirectory, file): Promise<FsUploadResult> {
      if (file.size > MAX_UPLOAD_BYTES)
        throw new FsWorkspaceError('TOO_LARGE', 'Upload exceeds the 1 GiB file limit')
      const filename = uploadFilename(file.name)
      const directoryPath = normalizedRelativePath(requestedDirectory)
      const directory = await resolvedInsideRoot(root, absolutePath(root, directoryPath))
      const directoryStat = await fs.stat(directory)
      if (!directoryStat.isDirectory())
        throw new FsWorkspaceError('NOT_DIRECTORY', 'Upload target is not a directory')

      const temporaryPath = path.join(directory, `.upload-${crypto.randomUUID()}.tmp`)
      try {
        await Bun.write(temporaryPath, file)
        return await publishTemporaryFile(directory, directoryPath, filename, temporaryPath, file.size)
      }
      catch (error) {
        await fs.unlink(temporaryPath).catch(() => {})
        if (error instanceof FsWorkspaceError)
          throw error
        throw new FsWorkspaceError('UNAVAILABLE', error instanceof Error ? error.message : String(error))
      }
    },
    async beginChunkedUpload(requestedDirectory, requestedFilename, size): Promise<FsChunkedUpload> {
      if (!Number.isSafeInteger(size) || size <= 0)
        throw new FsWorkspaceError('INVALID_UPLOAD', 'Upload size is invalid')
      const filename = uploadFilename(requestedFilename)
      const directoryPath = normalizedRelativePath(requestedDirectory)
      const directory = await resolvedInsideRoot(root, absolutePath(root, directoryPath))
      const directoryStat = await fs.stat(directory)
      if (!directoryStat.isDirectory())
        throw new FsWorkspaceError('NOT_DIRECTORY', 'Upload target is not a directory')
      await cleanupStaleUploadFiles(directory)

      const volumeKey = String(directoryStat.dev ?? 'default')
      const temporaryPath = await withReservationLock(async () => {
        const filesystem = await statfs(directory)
        const availableBytes = filesystem.bavail * filesystem.bsize
        const reservedBytes = Math.min(MAX_RESERVED_UPLOAD_BYTES, Math.floor(availableBytes * 0.1))
        const reservedForVolume = reservedUploadBytes.get(volumeKey) ?? 0
        if (size > availableBytes - reservedBytes - reservedForVolume)
          throw new FsWorkspaceError('INSUFFICIENT_SPACE', 'Upload does not fit in the available disk space')

        const target = path.join(directory, `.upload-${crypto.randomUUID()}.tmp`)
        let handle: Awaited<ReturnType<typeof fs.open>> | undefined
        try {
          handle = await fs.open(target, 'wx')
          await handle.close()
          handle = undefined
        }
        catch (cause) {
          await handle?.close().catch(() => {})
          await fs.unlink(target).catch(() => {})
          throw cause
        }
        reservedUploadBytes.set(volumeKey, reservedForVolume + size)
        return target
      })
      let released = false
      let published = false
      let publishedResult: FsUploadResult | undefined
      let publishing: Promise<FsUploadResult> | undefined
      const release = (): void => {
        if (released)
          return
        released = true
        const remaining = (reservedUploadBytes.get(volumeKey) ?? size) - size
        if (remaining <= 0)
          reservedUploadBytes.delete(volumeKey)
        else
          reservedUploadBytes.set(volumeKey, remaining)
      }
      return {
        async writeChunk(offset, stream, expectedLength, options = {}): Promise<void> {
          if (!Number.isSafeInteger(offset) || !Number.isSafeInteger(expectedLength) || offset < 0 || expectedLength <= 0 || offset + expectedLength > size)
            throw new FsWorkspaceError('INVALID_UPLOAD', 'Upload chunk range is invalid')
          const reader = stream.getReader()
          const abort = (): void => {
            void reader.cancel(options.signal?.reason).catch(() => {})
          }
          const throwIfAborted = (): void => {
            if (options.signal?.aborted)
              throw options.signal.reason ?? new FsWorkspaceError('UNAVAILABLE', 'Upload chunk was cancelled')
          }
          options.signal?.addEventListener('abort', abort, { once: true })
          let handle: Awaited<ReturnType<typeof fs.open>> | undefined
          let written = 0
          try {
            throwIfAborted()
            handle = await fs.open(temporaryPath, 'r+')
            while (true) {
              throwIfAborted()
              const chunk = await reader.read()
              if (chunk.done)
                break
              throwIfAborted()
              if (!(chunk.value instanceof Uint8Array) || written + chunk.value.byteLength > expectedLength)
                throw new FsWorkspaceError('INVALID_UPLOAD', 'Upload chunk body does not match its declared length')
              let chunkOffset = 0
              while (chunkOffset < chunk.value.byteLength) {
                throwIfAborted()
                const result = await handle.write(chunk.value, chunkOffset, chunk.value.byteLength - chunkOffset, offset + written + chunkOffset)
                if (result.bytesWritten <= 0)
                  throw new FsWorkspaceError('UNAVAILABLE', 'Upload chunk could not be written')
                chunkOffset += result.bytesWritten
              }
              written += chunk.value.byteLength
            }
            if (written !== expectedLength)
              throw new FsWorkspaceError('INVALID_UPLOAD', 'Upload chunk body does not match its declared length')
            await handle.sync()
          }
          finally {
            options.signal?.removeEventListener('abort', abort)
            await handle?.close().catch(() => {})
            reader.releaseLock()
          }
        },
        async publish(): Promise<FsUploadResult> {
          if (published)
            return publishedResult!
          if (publishing)
            return await publishing
          publishing = (async () => {
            try {
              const stat = await fs.stat(temporaryPath)
              if (stat.size !== size)
                throw new FsWorkspaceError('INVALID_UPLOAD', 'Upload is incomplete')
              const result = await publishTemporaryFile(directory, directoryPath, filename, temporaryPath, size)
              published = true
              publishedResult = result
              release()
              return result
            }
            finally {
              publishing = undefined
            }
          })()
          return await publishing
        },
        async abort(): Promise<void> {
          if (!published)
            await fs.unlink(temporaryPath).catch(() => {})
          release()
        },
      }
    },
    async writeFileStream(requestedDirectory, requestedFilename, stream, options: FsStreamWriteOptions = {}): Promise<FsUploadResult> {
      const filename = uploadFilename(requestedFilename)
      const directoryPath = normalizedRelativePath(requestedDirectory)
      const directory = await resolvedInsideRoot(root, absolutePath(root, directoryPath))
      const directoryStat = await fs.stat(directory)
      if (!directoryStat.isDirectory())
        throw new FsWorkspaceError('NOT_DIRECTORY', 'Download target is not a directory')

      const temporaryPath = path.join(directory, `.download-${crypto.randomUUID()}.tmp`)
      const reader = stream.getReader()
      const onAbort = (): void => {
        void reader.cancel(options.signal?.reason).catch(() => {})
      }
      options.signal?.addEventListener('abort', onAbort, { once: true })
      let handle: Awaited<ReturnType<typeof fs.open>> | undefined
      let size = 0
      try {
        handle = await fs.open(temporaryPath, 'w')
        while (true) {
          options.signal?.throwIfAborted()
          const chunk = await reader.read()
          if (chunk.done)
            break
          if (!(chunk.value instanceof Uint8Array))
            throw new FsWorkspaceError('UNAVAILABLE', 'Download response contained invalid data')
          let offset = 0
          while (offset < chunk.value.byteLength) {
            const result = await handle.write(chunk.value, { offset })
            if (result.bytesWritten <= 0)
              throw new FsWorkspaceError('UNAVAILABLE', 'Download could not be written')
            offset += result.bytesWritten
          }
          size += chunk.value.byteLength
          options.onProgress?.(size)
        }
        options.signal?.throwIfAborted()
        await handle.close()
        handle = undefined
        return await publishTemporaryFile(directory, directoryPath, filename, temporaryPath, size)
      }
      catch (error) {
        await reader.cancel(error).catch(() => {})
        await handle?.close().catch(() => {})
        await fs.unlink(temporaryPath).catch(() => {})
        throw error
      }
      finally {
        options.signal?.removeEventListener('abort', onAbort)
        reader.releaseLock()
      }
    },
    async extractArchive(requestedPath, extractOptions = {}) {
      const source = await operationEntry(root, requestedPath)
      if (!isExtractableArchiveName(source.name))
        throw new FsWorkspaceError('UNSUPPORTED_ARCHIVE', 'File type is not supported for extraction')
      const sourceResolved = await resolvedInsideRoot(root, source.resolved)
      if (!(await fs.stat(sourceResolved)).isFile())
        throw new FsWorkspaceError('NOT_FILE', 'Archive path is not a file')

      const parentRelativePath = source.relativePath.split('/').slice(0, -1).join('/')
      const parent = await operationDirectory(root, parentRelativePath)
      await cleanupStaleExtractionDirectories(parent.resolved)
      const temporary = path.join(parent.resolved, `.extract-${crypto.randomUUID()}.tmp`)
      await fs.mkdir(temporary)
      try {
        const inspection = await archiveExtractor.extract(sourceResolved, temporary, extractOptions)
        extractOptions.signal?.throwIfAborted()
        await validateExtractedTree(temporary)
        extractOptions.signal?.throwIfAborted()
        const destination = await collisionDirectoryDestination(parent.resolved, archiveDestinationName(source.name))
        await fs.rename(temporary, destination.resolved)
        return {
          archivePath: source.relativePath,
          destinationPath: [parent.relativePath, destination.name].filter(Boolean).join('/'),
          ...inspection,
        }
      }
      catch (cause) {
        await fs.rm(temporary, { recursive: true, force: true }).catch(() => {})
        if (cause instanceof FsWorkspaceError || extractOptions.signal?.aborted)
          throw cause
        throw new FsWorkspaceError('UNAVAILABLE', 'Archive extraction failed')
      }
    },
    async applyOperation(operation: FsOperation) {
      if (operation.action === 'create-directory') {
        let destinationPath: string | undefined
        try {
          const parent = await operationDirectory(root, operation.parentPath)
          const name = operationName(operation.name)
          destinationPath = [parent.relativePath, name].filter(Boolean).join('/')
          try {
            await fs.mkdir(path.join(parent.resolved, name))
          }
          catch (cause) {
            if ((cause as NodeJS.ErrnoException).code === 'EEXIST')
              throw new FsWorkspaceError('ALREADY_EXISTS', 'An entry with that name already exists')
            throw cause
          }
          return { action: operation.action, items: [{ status: 'ok' as const, destinationPath }] }
        }
        catch (cause) {
          return { action: operation.action, items: [operationFailure(cause, { destinationPath })] }
        }
      }

      if (operation.action === 'rename') {
        let destinationPath: string | undefined
        try {
          const source = await operationEntry(root, operation.path)
          const name = operationName(operation.newName)
          const parentPath = source.relativePath.split('/').slice(0, -1).join('/')
          destinationPath = [parentPath, name].filter(Boolean).join('/')
          const destination = path.join(path.dirname(source.resolved), name)
          await requireMissing(destination)
          await fs.rename(source.resolved, destination)
          return {
            action: operation.action,
            items: [{ status: 'ok' as const, sourcePath: source.relativePath, destinationPath }],
          }
        }
        catch (cause) {
          return {
            action: operation.action,
            items: [operationFailure(cause, { sourcePath: operation.path, destinationPath })],
          }
        }
      }

      if (operation.action === 'copy') {
        let destination: Awaited<ReturnType<typeof operationDirectory>>
        try {
          destination = await operationDirectory(root, operation.destinationPath)
        }
        catch (cause) {
          return {
            action: operation.action,
            items: operation.paths.map(sourcePath => operationFailure(cause, { sourcePath })),
          }
        }
        const items: FsOperationItem[] = []
        for (const requestedPath of operation.paths) {
          try {
            const source = await operationEntry(root, requestedPath)
            const sourceStat = await fs.lstat(source.resolved)
            if (sourceStat.isDirectory()) {
              const sourceDirectory = await fs.realpath(source.resolved)
              if (isWithinRoot(sourceDirectory, destination.resolved))
                throw new FsWorkspaceError('INVALID_OPERATION', 'A directory cannot be copied into itself')
            }
            const target = await collisionDestination(destination.resolved, source.name)
            await fs.cp(source.resolved, target.resolved, {
              recursive: sourceStat.isDirectory(),
              dereference: false,
              errorOnExist: true,
              force: false,
            })
            items.push({
              status: 'ok',
              sourcePath: source.relativePath,
              destinationPath: [destination.relativePath, target.filename].filter(Boolean).join('/'),
            })
          }
          catch (cause) {
            items.push(operationFailure(cause, { sourcePath: requestedPath }))
          }
        }
        return { action: operation.action, items }
      }

      if (operation.action === 'move') {
        let destination: Awaited<ReturnType<typeof operationDirectory>>
        try {
          destination = await operationDirectory(root, operation.destinationPath)
        }
        catch (cause) {
          return {
            action: operation.action,
            items: operation.paths.map(sourcePath => operationFailure(cause, { sourcePath })),
          }
        }
        const items: FsOperationItem[] = []
        for (const requestedPath of operation.paths) {
          let destinationPath: string | undefined
          try {
            const source = await operationEntry(root, requestedPath)
            const sourceStat = await fs.lstat(source.resolved)
            if (sourceStat.isDirectory()) {
              const sourceDirectory = await fs.realpath(source.resolved)
              if (isWithinRoot(sourceDirectory, destination.resolved))
                throw new FsWorkspaceError('INVALID_OPERATION', 'A directory cannot be moved into itself')
            }
            destinationPath = [destination.relativePath, source.name].filter(Boolean).join('/')
            const target = path.join(destination.resolved, source.name)
            await requireMissing(target)
            await fs.rename(source.resolved, target)
            items.push({ status: 'ok', sourcePath: source.relativePath, destinationPath })
          }
          catch (cause) {
            items.push(operationFailure(cause, { sourcePath: requestedPath, destinationPath }))
          }
        }
        return { action: operation.action, items }
      }

      if (operation.action === 'delete') {
        const items: FsOperationItem[] = []
        for (const requestedPath of operation.paths) {
          try {
            const source = await operationEntry(root, requestedPath)
            const sourceStat = await fs.lstat(source.resolved)
            await fs.rm(source.resolved, { recursive: sourceStat.isDirectory(), force: false })
            items.push({ status: 'ok', sourcePath: source.relativePath })
          }
          catch (cause) {
            items.push(operationFailure(cause, { sourcePath: requestedPath }))
          }
        }
        return { action: operation.action, items }
      }

      throw new FsWorkspaceError('INVALID_OPERATION', 'Operation is not supported')
    },
  }
}
