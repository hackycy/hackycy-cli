import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { verifyAssetGraph, verifyRequiredWorkerAssets } from './asset-graph.mjs'

const root = resolve(fileURLToPath(new URL('.', import.meta.url)), 'dist')
const graph = await verifyAssetGraph(root)
verifyRequiredWorkerAssets(graph)
console.log(`verified ${graph.shells.length} Vite shells and ${graph.assets.length} reachable generated assets`)
