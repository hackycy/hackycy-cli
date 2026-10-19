import type { HunksRenderResult } from '@pierre/diffs'
import { Worker as NodeWorker } from 'node:worker_threads'
import { DiffHunksRenderer } from '@pierre/diffs'
import { WorkerPoolManager } from '@pierre/diffs/worker'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { parseTextDiff } from './text-diff'

// Run the same portable worker shipped to the browser; only its message
// transport is adapted to Node so the real highlighter and cache stay in use.
function createPortableWorker(): Worker {
  const bootstrap = `
    import { parentPort } from 'node:worker_threads';
    globalThis.self = {
      addEventListener(type, listener) {
        parentPort.on(type, data => listener(type === 'message' ? { data } : data));
      },
    };
    globalThis.postMessage = data => parentPort.postMessage(data);
    await import(${JSON.stringify(import.meta.resolve('@pierre/diffs/worker/worker-portable.js'))});
  `
  const worker = new NodeWorker(new URL(`data:text/javascript,${encodeURIComponent(bootstrap)}`))
  const transport = Object.assign(new EventTarget(), {
    onmessage: null,
    onmessageerror: null,
    onerror: null,
    postMessage: (message: unknown) => worker.postMessage(message),
    terminate: () => { void worker.terminate() },
  })
  worker.on('message', data => transport.dispatchEvent(new MessageEvent('message', { data })))
  worker.on('error', error => transport.dispatchEvent(Object.assign(new Event('error'), { error, message: error.message })))
  return transport
}

function astText(node: unknown): string {
  if (typeof node !== 'object' || node === null)
    return ''
  if ('value' in node && typeof node.value === 'string')
    return node.value
  if ('children' in node && Array.isArray(node.children))
    return node.children.map(astText).join('')
  return ''
}

function renderedText(result: HunksRenderResult): string {
  return [result.unifiedContentAST, result.deletionsContentAST, result.additionsContentAST]
    .flatMap(nodes => nodes ?? [])
    .map(astText)
    .join('')
}

const animationFrames = new Set<ReturnType<typeof setTimeout>>()

beforeEach(() => {
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    const timer = setTimeout(() => {
      animationFrames.delete(timer)
      callback(performance.now())
    }, 0)
    animationFrames.add(timer)
    return timer
  })
  vi.stubGlobal('cancelAnimationFrame', clearTimeout)
})

afterEach(() => {
  for (const timer of animationFrames)
    clearTimeout(timer)
  animationFrames.clear()
  vi.unstubAllGlobals()
})

const fixtures = [
  { name: 'line endings', baseline: 'a\n', target: 'a\r\n', ignoredRows: 0, expectedText: ['a'] },
  { name: 'trailing whitespace', baseline: 'a \n', target: 'a\n', ignoredRows: 0, expectedText: ['a'] },
  { name: 'text changes', baseline: 'before \n', target: 'after\n', ignoredRows: 1, expectedText: ['before', 'after'] },
]

it('scopes parsed diff identity by snapshot, entry and comparison options while preserving source text', () => {
  const input = { snapshotId: 'snapshot', entryId: 1, path: 'example.ts', baseline: 'a\n', target: 'a\r\n', ignoreWhitespace: false }
  const diff = parseTextDiff(input)
  expect(parseTextDiff(input).cacheKey).toBe(diff.cacheKey)
  expect(parseTextDiff({ ...input, snapshotId: 'next' }).cacheKey).not.toBe(diff.cacheKey)
  expect(parseTextDiff({ ...input, entryId: 2 }).cacheKey).not.toBe(diff.cacheKey)
  expect(parseTextDiff({ ...input, ignoreWhitespace: true }).cacheKey).not.toBe(diff.cacheKey)
  expect(diff.deletionLines).toEqual(['a\n'])
  expect(diff.additionLines).toEqual(['a\r\n'])
})

describe.each(['split', 'unified'] as const)('text diff with the real worker (%s)', (diffStyle) => {
  describe.each([true, false])('reuse renderer: %s', (reuseRenderer) => {
    it.each(fixtures)('updates $name in both directions without reusing incompatible cached lines', async (fixture) => {
      const pool = new WorkerPoolManager({ poolSize: 1, workerFactory: createPortableWorker }, {})
      let renderer: DiffHunksRenderer | undefined
      let onUpdate = (): void => {}
      try {
        await pool.initialize()
        for (const [index, ignoreWhitespace] of [true, false, true, false].entries()) {
          if (!renderer || !reuseRenderer) {
            renderer?.cleanUp()
            renderer = new DiffHunksRenderer({ diffStyle, disableFileHeader: true }, () => onUpdate(), pool)
          }
          renderer.setOptions({ diffStyle, disableFileHeader: true, overflow: index % 2 ? 'wrap' : 'scroll' })
          const ready = Promise.withResolvers<void>()
          onUpdate = ready.resolve
          const diff = parseTextDiff({
            snapshotId: 'snapshot',
            entryId: 1,
            path: 'example.ts',
            baseline: fixture.baseline,
            target: fixture.target,
            ignoreWhitespace,
          })
          renderer.renderDiff(diff)
          if (!pool.getDiffResultCache(diff))
            await ready.promise
          const result = renderer.renderDiff(diff)
          if (!result)
            throw new Error('Worker did not produce a diff')
          const expectedRows = (ignoreWhitespace ? fixture.ignoredRows : 1) * (diffStyle === 'unified' ? 2 : 1)
          expect(result.rowCount).toBe(expectedRows)
          if (expectedRows > 0) {
            for (const text of fixture.expectedText)
              expect(renderedText(result)).toContain(text)
          }
          else {
            expect(renderedText(result)).toBe('')
          }
        }
      }
      finally {
        renderer?.cleanUp()
        pool.terminate()
      }
    })
  })
})
