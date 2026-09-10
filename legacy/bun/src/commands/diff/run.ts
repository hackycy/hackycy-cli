import { realpath } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { startDiffHttpServer } from './server'
import { createComparisonWorkspace } from './workspace'

export interface DiffCommandOptions {
  baselineDirectory: string
  targetDirectory: string
  public: boolean
  port: number
  exclude: string[]
  gitignore: boolean
}

export function formatDiffUrls(
  publicAccess: boolean,
  port: string,
  interfaces = os.networkInterfaces(),
): string[] {
  const urls = [`http://${publicAccess ? 'localhost' : '127.0.0.1'}:${port}`]
  if (!publicAccess)
    return urls

  for (const networks of Object.values(interfaces)) {
    if (!networks)
      continue
    for (const network of networks) {
      if (network.family === 'IPv4' && !network.internal)
        urls.push(`http://${network.address}:${port}`)
    }
  }
  return urls
}

export async function runDiffCommand(options: DiffCommandOptions): Promise<void> {
  const [baselineDirectory, targetDirectory] = await Promise.all([
    realpath(path.resolve(options.baselineDirectory)),
    realpath(path.resolve(options.targetDirectory)),
  ])
  const workspace = await createComparisonWorkspace({
    baselineDirectory,
    targetDirectory,
    gitignore: options.gitignore,
    exclusions: options.exclude,
  })
  const server = startDiffHttpServer({
    workspace,
    address: options.public ? '0.0.0.0' : '127.0.0.1',
    port: options.port,
    initialRefresh: true,
  })

  const [localUrl, ...networkUrls] = formatDiffUrls(options.public, server.url.port)
  console.log(`Directory diff: ${localUrl}`)
  console.log(`MCP endpoint:   ${localUrl}/mcp`)
  for (const url of networkUrls)
    console.log(`Network: ${url}`)
  for (const url of networkUrls)
    console.log(`Network MCP: ${url}/mcp`)
  console.log(`Baseline: ${baselineDirectory}`)
  console.log(`Target:   ${targetDirectory}`)

  const stop = async (): Promise<void> => {
    await server.stop()
    process.exit(0)
  }
  process.once('SIGINT', stop)
  process.once('SIGTERM', stop)

  await server.finished
}
