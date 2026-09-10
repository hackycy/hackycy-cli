import os from 'node:os'
import path from 'node:path'
import process from 'node:process'

function home(env: NodeJS.ProcessEnv): string {
  return env.USERPROFILE || env.HOME || os.homedir()
}

export function applicationStateRoot(env: NodeJS.ProcessEnv = process.env, platform: NodeJS.Platform = process.platform): string {
  if (platform === 'win32')
    return env.LOCALAPPDATA || path.join(home(env), 'AppData', 'Local')
  if (platform === 'darwin')
    return path.join(home(env), 'Library', 'Application Support')
  return env.XDG_STATE_HOME || path.join(home(env), '.local', 'state')
}
