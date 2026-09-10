import { describe, expect, test } from 'bun:test'
import { ContentCache, estimateCacheBytes } from './content-cache'

describe('ContentCache', () => {
  test('evicts least recently used values within its byte budget', () => {
    const cache = new ContentCache(11)
    cache.set('a', 'first', 6)
    cache.set('b', 'second', 4)
    expect(cache.get<string>('a')).toBe('first')

    cache.set('c', 'third', 5)
    expect(cache.get<string>('b')).toBeUndefined()
    expect(cache.get<string>('a')).toBe('first')
    expect(cache.get<string>('c')).toBe('third')
    expect(cache.sizeBytes).toBe(11)
  })

  test('does not retain individual values larger than the budget and clears atomically', () => {
    const cache = new ContentCache(8)
    cache.set('large', 'value', 9)
    expect(cache.get('large')).toBeUndefined()
    cache.set('small', 'value', 4)
    cache.clear()
    expect(cache.sizeBytes).toBe(0)
    expect(cache.get('small')).toBeUndefined()
  })

  test('estimates decoded strings using their in-memory UTF-16 size', () => {
    expect(estimateCacheBytes({ text: 'abcd' })).toBe(264)
  })
})
