import { readdir, readFile, stat } from 'node:fs/promises'
import { dirname, relative, resolve, sep } from 'node:path'

export const requiredShells = ['diff/index.html', 'fs/index.html', 'tunnel-server/index.html']

const requiredWorkerAssetPrefixes = [
  ['Monaco CSS worker', 'css.worker-'],
  ['Monaco editor worker', 'editor.worker-'],
  ['Monaco HTML worker', 'html.worker-'],
  ['Monaco JSON worker', 'json.worker-'],
  ['Monaco TypeScript worker', 'typescript.worker-'],
  ['Monaco worker runtime', 'editorWebWorkerMain-'],
  ['Monaco font asset', 'codicon-'],
  ['Pierre diff worker', 'worker-portable-'],
]

async function exists(path) {
  try {
    return (await stat(path)).isFile()
  }
  catch {
    return false
  }
}

async function filesBelow(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const files = []
  for (const entry of entries) {
    const path = resolve(directory, entry.name)
    if (entry.isDirectory())
      files.push(...await filesBelow(path))
    else if (entry.isFile())
      files.push(path)
  }
  return files
}

function outputPath(root, reference, sourcePath = root) {
  const pathReference = reference.split(/[?#]/, 1)[0]
  let output
  if (pathReference.startsWith('/assets/'))
    output = resolve(root, `.${pathReference}`)
  else if (pathReference.startsWith('./') || pathReference.startsWith('../'))
    output = resolve(dirname(sourcePath), pathReference)
  else
    return null

  const outputRelative = relative(root, output)
  if (outputRelative === '' || outputRelative.startsWith(`..${sep}`) || outputRelative === '..')
    throw new Error(`generated reference escapes dist: ${reference}`)
  return output
}

function manifestEntry(manifest, shell) {
  const matches = Object.entries(manifest).filter(([key, value]) => key === shell || value?.src === shell)
  if (matches.length !== 1)
    throw new Error(`manifest must contain exactly one entry for shell: ${shell}`)
  return matches[0][0]
}

function manifestFiles(item) {
  return [item.file, ...(item.css ?? []), ...(item.assets ?? [])]
}

function manifestImports(item) {
  return [...(item.imports ?? []), ...(item.dynamicImports ?? [])]
}

export async function verifyAssetGraph(distRoot) {
  const root = resolve(distRoot)
  if (!await exists(resolve(root, '.vite/manifest.json')))
    throw new Error('missing Vite manifest: dist/.vite/manifest.json')
  if (!await exists(resolve(root, 'assets/.keep'))) {
    try {
      await stat(resolve(root, 'assets'))
    }
    catch {
      throw new Error('missing generated asset directory: dist/assets')
    }
  }

  const manifestPath = resolve(root, '.vite/manifest.json')
  const manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
  const referenced = new Set()
  const visitedEntries = new Set()

  async function addReferencedFile(path, description) {
    const asset = relative(root, path).split(sep).join('/')
    if (!asset.startsWith('assets/'))
      throw new Error(`${description} is outside the generated asset tree: ${asset}`)
    if (!await exists(path))
      throw new Error(`${description} is missing: ${asset}`)
    referenced.add(asset)
  }

  async function collectEntryFiles(entry) {
    if (visitedEntries.has(entry))
      return
    visitedEntries.add(entry)
    const item = manifest[entry]
    if (!item)
      throw new Error(`manifest references missing entry: ${entry}`)
    for (const file of manifestFiles(item)) {
      if (typeof file !== 'string')
        throw new Error(`manifest entry has an invalid generated file: ${entry}`)
      const output = outputPath(root, `/${file}`)
      if (!output)
        throw new Error(`manifest entry has an invalid generated file: ${file}`)
      await addReferencedFile(output, 'manifest generated asset')
    }
    for (const imported of manifestImports(item)) {
      if (typeof imported !== 'string')
        throw new Error(`manifest entry has an invalid import: ${entry}`)
      await collectEntryFiles(imported)
    }
  }

  for (const shell of requiredShells) {
    const shellPath = resolve(root, shell)
    if (!await exists(shellPath))
      throw new Error(`missing required shell: dist/${shell}`)

    const html = await readFile(shellPath, 'utf8')
    for (const match of html.matchAll(/\b(?:src|href)=["']([^"']+)["']/g)) {
      const assetPath = outputPath(root, match[1], shellPath)
      if (assetPath)
        await addReferencedFile(assetPath, `shell ${shell} asset`)
    }
    await collectEntryFiles(manifestEntry(manifest, shell))
  }

  const workerScanSeen = new Set()
  async function collectWorkerFiles(asset) {
    if (workerScanSeen.has(asset) || !asset.endsWith('.js'))
      return
    workerScanSeen.add(asset)

    const assetPath = resolve(root, asset)
    const source = await readFile(assetPath, 'utf8')
    const workerReferences = /new\s+Worker\s*\(\s*(?:new\s+URL\s*\(\s*)?(["'`])([^"'`]+)\1/g
    for (const match of source.matchAll(workerReferences)) {
      const workerPath = outputPath(root, match[2], assetPath)
      if (!workerPath)
        continue
      await addReferencedFile(workerPath, 'generated worker reference')
      await collectWorkerFiles(relative(root, workerPath).split(sep).join('/'))
    }
  }

  for (const asset of [...referenced])
    await collectWorkerFiles(asset)

  const assets = await filesBelow(resolve(root, 'assets'))
  for (const assetPath of assets) {
    const asset = relative(root, assetPath).split(sep).join('/')
    if (asset.endsWith('.map'))
      throw new Error(`source map was emitted: ${asset}`)
    if (!referenced.has(asset))
      throw new Error(`unreferenced generated asset: ${asset}`)
  }

  return { assets: [...referenced].sort(), shells: [...requiredShells] }
}

export function verifyRequiredWorkerAssets(graph) {
  for (const [description, prefix] of requiredWorkerAssetPrefixes) {
    const matches = graph.assets.filter(asset => asset.startsWith(`assets/${prefix}`))
    if (matches.length !== 1)
      throw new Error(`${description} must appear exactly once in the Vite asset graph`)
  }
}
