const DEFAULT_CONTENT_CACHE_BYTES = 32 * 1024 * 1024

interface CacheRecord {
  value: unknown
  bytes: number
}

export class ContentCache {
  private readonly records = new Map<string, CacheRecord>()
  private usedBytes = 0

  constructor(private readonly maximumBytes = DEFAULT_CONTENT_CACHE_BYTES) {}

  get sizeBytes(): number {
    return this.usedBytes
  }

  get<T>(key: string): T | undefined {
    const record = this.records.get(key)
    if (!record)
      return undefined
    this.records.delete(key)
    this.records.set(key, record)
    return record.value as T
  }

  set<T>(key: string, value: T, bytes: number): void {
    const existing = this.records.get(key)
    if (existing) {
      this.records.delete(key)
      this.usedBytes -= existing.bytes
    }
    if (bytes > this.maximumBytes)
      return

    this.records.set(key, { value, bytes })
    this.usedBytes += bytes
    while (this.usedBytes > this.maximumBytes) {
      const oldestKey = this.records.keys().next().value as string | undefined
      if (oldestKey === undefined)
        break
      const oldest = this.records.get(oldestKey)!
      this.records.delete(oldestKey)
      this.usedBytes -= oldest.bytes
    }
  }

  clear(): void {
    this.records.clear()
    this.usedBytes = 0
  }
}

export function estimateCacheBytes(value: unknown): number {
  if (typeof value === 'object' && value !== null && 'text' in value && typeof value.text === 'string')
    return value.text.length * 2 + 256
  return JSON.stringify(value).length * 2 + 128
}

export const contentCache = new ContentCache()
