import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { verifyAssetGraph, verifyRequiredWorkerAssets } from './asset-graph.mjs'

const roots: string[] = []

afterEach(async () => {
  await Promise.all(roots.splice(0).map(root => rm(root, { force: true, recursive: true })))
})

async function graphFixture(): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), 'ycy-asset-graph-'))
  roots.push(root)
  await Promise.all([
    mkdir(join(root, '.vite'), { recursive: true }),
    mkdir(join(root, 'assets'), { recursive: true }),
    mkdir(join(root, 'diff'), { recursive: true }),
    mkdir(join(root, 'fs'), { recursive: true }),
    mkdir(join(root, 'tunnel-server'), { recursive: true }),
  ])
  await Promise.all([
    writeFile(join(root, '.vite/manifest.json'), JSON.stringify({
      'diff/index.html': { file: 'assets/diff.js' },
      'fs/index.html': { file: 'assets/fs.js' },
      'tunnel-server/index.html': { file: 'assets/tunnel.js' },
    })),
    writeFile(join(root, 'diff/index.html'), '<script src="/assets/diff.js"></script>'),
    writeFile(join(root, 'fs/index.html'), '<script src="/assets/fs.js"></script>'),
    writeFile(join(root, 'tunnel-server/index.html'), '<script src="/assets/tunnel.js"></script>'),
    writeFile(join(root, 'assets/diff.js'), 'new Worker("/assets/diff.worker.js")'),
    writeFile(join(root, 'assets/fs.js'), ''),
    writeFile(join(root, 'assets/tunnel.js'), ''),
    writeFile(join(root, 'assets/diff.worker.js'), ''),
  ])
  return root
}

describe('vite asset graph', () => {
  it('accepts a complete three-entry graph including worker assets', async () => {
    const root = await graphFixture()

    await expect(verifyAssetGraph(root)).resolves.toMatchObject({
      assets: ['assets/diff.js', 'assets/diff.worker.js', 'assets/fs.js', 'assets/tunnel.js'],
      shells: ['diff/index.html', 'fs/index.html', 'tunnel-server/index.html'],
    })
  })

  it('fails closed for missing shells, missing manifest output, and stale assets', async () => {
    const root = await graphFixture()
    await rm(join(root, 'fs/index.html'))
    await expect(verifyAssetGraph(root)).rejects.toThrow('missing required shell: dist/fs/index.html')

    await writeFile(join(root, 'fs/index.html'), '<script src="/assets/fs.js"></script>')
    await rm(join(root, 'assets/tunnel.js'))
    await expect(verifyAssetGraph(root)).rejects.toThrow('asset is missing: assets/tunnel.js')

    await writeFile(join(root, 'assets/tunnel.js'), '')
    await writeFile(join(root, 'assets/stale.js'), '')
    await expect(verifyAssetGraph(root)).rejects.toThrow('unreferenced generated asset: assets/stale.js')
  })

  it('requires every current Monaco and Pierre worker artifact', () => {
    const assets = [
      'assets/css.worker-a.js',
      'assets/editor.worker-a.js',
      'assets/html.worker-a.js',
      'assets/json.worker-a.js',
      'assets/typescript.worker-a.js',
      'assets/editorWebWorkerMain-a.js',
      'assets/codicon-a.ttf',
      'assets/worker-portable-a.js',
    ]

    expect(() => verifyRequiredWorkerAssets({ assets })).not.toThrow()
    expect(() => verifyRequiredWorkerAssets({ assets: assets.slice(1) })).toThrow('Monaco CSS worker must appear exactly once')
  })
})
