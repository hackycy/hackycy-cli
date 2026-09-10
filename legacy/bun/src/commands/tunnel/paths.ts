import path from 'node:path'
import process from 'node:process'
import { applicationStateRoot } from '../../shared/application-state'
import { FRP_VERSION } from './frp/manifest'

export function defaultServerDataDirectory(env: NodeJS.ProcessEnv = process.env, platform: NodeJS.Platform = process.platform): string {
  return path.join(applicationStateRoot(env, platform), 'ycy', 'tunnel', 'server')
}

export function clientStateDirectory(env: NodeJS.ProcessEnv = process.env, platform: NodeJS.Platform = process.platform): string {
  return path.join(applicationStateRoot(env, platform), 'ycy', 'tunnel', 'client')
}

export function managedFrpDirectory(env: NodeJS.ProcessEnv = process.env, platform: NodeJS.Platform = process.platform): string {
  if (env.YCY_TUNNEL_DOCKER === '1')
    return path.join('/opt', 'ycy', 'frp', FRP_VERSION)
  return path.join(applicationStateRoot(env, platform), 'ycy', 'frp', FRP_VERSION)
}

export function managedFrpBinaryPath(role: 'frpc' | 'frps', env: NodeJS.ProcessEnv = process.env, platform: NodeJS.Platform = process.platform): string {
  return path.join(managedFrpDirectory(env, platform), platform === 'win32' ? `${role}.exe` : role)
}
