import type { DownloadTask, ExtractionTask } from './api'
import { describe, expect, it } from 'vitest'
import { downloadDetail, extractionDetail, formatBytes } from './components/activity-center'

function task(update: Partial<DownloadTask>): DownloadTask {
  return {
    id: 'task',
    url: 'https://example.test/file',
    directoryPath: '',
    status: 'running',
    bytesDownloaded: 0,
    createdAt: '2026-01-01T00:00:00.000Z',
    ...update,
  }
}

describe('download activity presentation', () => {
  it('shows transferred bytes, total size, and speed for active downloads', () => {
    expect(downloadDetail(task({
      bytesDownloaded: 5 * 1024 * 1024,
      totalBytes: 20 * 1024 * 1024,
      speedBytesPerSecond: 2 * 1024 * 1024,
    }))).toBe('5.0 MiB / 20 MiB · 2.0 MiB/s')
  })

  it('keeps unknown totals and terminal states readable', () => {
    expect(downloadDetail(task({ bytesDownloaded: 1536 }))).toBe('1.5 KiB')
    expect(downloadDetail(task({ status: 'cancelled' }))).toBe('Cancelled')
    expect(downloadDetail(task({ status: 'error', error: 'Remote server returned HTTP 503' }))).toBe('Remote server returned HTTP 503')
    expect(formatBytes(0)).toBe('0 B')
  })
})

describe('extraction activity presentation', () => {
  const extraction = (update: Partial<ExtractionTask>): ExtractionTask => ({
    id: 'extract',
    archivePath: 'backups/archive.tar.gz',
    status: 'running',
    createdAt: '2026-01-01T00:00:00.000Z',
    ...update,
  })

  it('shows inspection, progress metadata, destination, cancellation, and failures', () => {
    expect(extractionDetail(extraction({}))).toBe('Checking archive')
    expect(extractionDetail(extraction({ progress: 30, uncompressedBytes: 2048, entryCount: 3 }))).toBe('2.0 KiB · 3 entries')
    expect(extractionDetail(extraction({ status: 'done', destinationPath: 'backups/archive' }))).toBe('Extracted to /backups/archive')
    expect(extractionDetail(extraction({ status: 'cancelled' }))).toBe('Cancelled')
    expect(extractionDetail(extraction({ status: 'error', error: 'Archive is damaged' }))).toBe('Archive is damaged')
  })
})
