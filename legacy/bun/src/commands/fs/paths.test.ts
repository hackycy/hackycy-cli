import { describe, expect, test } from 'bun:test'
import { defaultFsSessionDirectory, resolveFsChunkedUploadOptions, resolveFsSessionOptions } from './paths'

describe('fs session paths', () => {
  test('uses CLI values before environment values and isolates default state by root', () => {
    const env = { HOME: '/Users/test', YCY_FS_SESSION_DIR: '/env-sessions', YCY_FS_SESSION_IDLE_DAYS: '3' }

    expect(resolveFsSessionOptions({ sessionDir: '/cli-sessions', sessionIdleDays: 2 }, '/workspace', env)).toEqual({
      directory: '/cli-sessions',
      idleLifetimeMs: 2 * 24 * 60 * 60 * 1000,
    })
    expect(resolveFsSessionOptions({}, '/workspace', env)).toEqual({
      directory: '/env-sessions',
      idleLifetimeMs: 3 * 24 * 60 * 60 * 1000,
    })
    expect(defaultFsSessionDirectory('/workspace', { HOME: '/Users/test' }, 'darwin')).not.toBe(defaultFsSessionDirectory('/other-workspace', { HOME: '/Users/test' }, 'darwin'))
  })

  test('rejects an invalid idle lifetime', () => {
    expect(() => resolveFsSessionOptions({ sessionIdleDays: 0 }, '/workspace', {})).toThrow('positive integer')
  })

  test('resolves chunked uploads from CLI values before environment values', () => {
    const env = { YCY_FS_CHUNKED_UPLOAD: '1', YCY_FS_UPLOAD_CHUNK_SIZE_MIB: '12' }
    expect(resolveFsChunkedUploadOptions({}, env)).toEqual({ enabled: true, chunkSizeBytes: 12 * 1024 * 1024 })
    expect(resolveFsChunkedUploadOptions({ chunkedUpload: false, uploadChunkSizeMiB: 4 }, env)).toEqual({ enabled: false, chunkSizeBytes: 4 * 1024 * 1024 })
    expect(() => resolveFsChunkedUploadOptions({ uploadChunkSizeMiB: 3 }, {})).toThrow('4 to 16 MiB')
  })
})
