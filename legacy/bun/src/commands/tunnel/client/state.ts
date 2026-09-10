import type { FrpcDesiredConfiguration } from '../types'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { atomicWrite } from '../atomic-file'

export interface AppliedClientState extends FrpcDesiredConfiguration {
  revision: number
}

export function appliedStatePath(stateDirectory: string): string {
  return path.join(stateDirectory, 'last-applied.json')
}

export function activeFrpcConfigPath(stateDirectory: string): string {
  return path.join(stateDirectory, 'frpc.toml')
}

export async function readAppliedClientState(stateDirectory: string): Promise<AppliedClientState | undefined> {
  try {
    const parsed = JSON.parse(await readFile(appliedStatePath(stateDirectory), 'utf8')) as AppliedClientState
    if (!Number.isSafeInteger(parsed.revision) || parsed.revision < 0 || parsed.snapshot.revision !== parsed.revision)
      return undefined
    return parsed
  }
  catch {
    return undefined
  }
}

export async function writeAppliedClientState(stateDirectory: string, state: AppliedClientState): Promise<void> {
  await atomicWrite(appliedStatePath(stateDirectory), `${JSON.stringify(state, null, 2)}\n`)
}
