import type { ServerTunnelConfig } from '../types'
import { Buffer } from 'node:buffer'
import { readFile, rm } from 'node:fs/promises'
import path from 'node:path'
import { getLogger } from '../../../shared/log'
import { atomicWrite } from '../atomic-file'
import { renderFrpsConfig } from '../frp/config'
import { TunnelError } from '../types'

export const MAX_CUSTOM_404_PAGE_BYTES = 512 * 1024

export class FrpsConfigurationPaths {
  readonly tomlPath: string
  readonly custom404PagePath: string

  constructor(dataDir: string) {
    const directory = path.resolve(dataDir)
    this.tomlPath = path.join(directory, 'frps.toml')
    this.custom404PagePath = path.join(directory, '404.html')
  }
}

function fileError(action: 'read' | 'write', cause: unknown): TunnelError {
  const message = cause instanceof Error ? cause.message : String(cause)
  return new TunnelError('CONFIGURATION_FAILED', `Could not ${action} custom 404 page: ${message}`)
}

function isMissingFile(cause: unknown): boolean {
  return typeof cause === 'object' && cause !== null && 'code' in cause && cause.code === 'ENOENT'
}

export class FrpsConfiguration {
  readonly paths: FrpsConfigurationPaths
  private queue = Promise.resolve()
  private readonly logger = getLogger('tunnel.server.frps')

  constructor(private readonly serverConfig: ServerTunnelConfig) {
    this.paths = new FrpsConfigurationPaths(serverConfig.dataDir)
  }

  get tomlPath(): string {
    return this.paths.tomlPath
  }

  render(internalFrpToken: string): string {
    return renderFrpsConfig(this.serverConfig, internalFrpToken, this.paths.custom404PagePath, this.logger.level)
  }

  async getCustom404Page(): Promise<string> {
    try {
      return await readFile(this.paths.custom404PagePath, 'utf8')
    }
    catch (cause) {
      if (isMissingFile(cause))
        return ''
      throw fileError('read', cause)
    }
  }

  async setCustom404Page(content: string): Promise<void> {
    if (Buffer.byteLength(content, 'utf8') > MAX_CUSTOM_404_PAGE_BYTES)
      throw new TunnelError('INVALID_CUSTOM_404_PAGE', `Custom 404 page must not exceed ${MAX_CUSTOM_404_PAGE_BYTES / 1024} KiB`)
    const result = this.queue.then(async () => {
      try {
        if (content)
          await atomicWrite(this.paths.custom404PagePath, content)
        else
          await rm(this.paths.custom404PagePath, { force: true })
      }
      catch (cause) {
        throw fileError('write', cause)
      }
    })
    this.queue = result.catch(() => {})
    await result
  }
}
