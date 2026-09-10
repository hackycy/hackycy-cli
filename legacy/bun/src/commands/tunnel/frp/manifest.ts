import type { FrpArtifactDescription } from '../types'
import process from 'node:process'
import { TunnelError } from '../types'
import { GENERATED_FRP_ARTIFACTS } from './manifest.generated'
import { FRP_VERSION } from './version'

export { FRP_VERSION } from './version'

export interface FrpArtifact extends FrpArtifactDescription {
  platform: 'darwin' | 'linux' | 'win32'
  architecture: 'x64' | 'arm64'
  frpsSha256: string
}

const BASE_URL = `https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}`
const ARTIFACTS: FrpArtifact[] = GENERATED_FRP_ARTIFACTS.map(artifact => ({
  ...artifact,
  version: FRP_VERSION,
  url: `${BASE_URL}/${artifact.archive}`,
}))

export function resolveFrpArtifact(platform: NodeJS.Platform = process.platform, architecture: NodeJS.Architecture = process.arch): FrpArtifact {
  const artifact = ARTIFACTS.find(candidate => candidate.platform === platform && candidate.architecture === architecture)
  if (!artifact)
    throw new TunnelError('UNSUPPORTED_PLATFORM', `FRP ${FRP_VERSION} is not available for ${platform}/${architecture}`)
  return artifact
}

export const FRP_ARTIFACTS: readonly FrpArtifact[] = ARTIFACTS
