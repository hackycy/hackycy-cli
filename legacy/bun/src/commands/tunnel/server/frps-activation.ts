import type { ServerTunnelConfig } from '../types'
import { TunnelError } from '../types'

export function frpsActivationError(config: ServerTunnelConfig, cause: unknown): TunnelError {
  const message = cause instanceof Error ? cause.message : String(cause)
  const bindAddress = `${config.address}:${config.frpPort}`
  const httpAddress = `${config.address}:${config.httpPort}`
  return new TunnelError(
    'ACTIVATION_FAILED',
    `Managed frps failed to start for FRP bind ${bindAddress} or HTTP vhost ${httpAddress}: ${message}. Stop any existing frps or other process listening on these ports before starting ycy. Inspect listeners with lsof -nP -iTCP:${config.frpPort} -sTCP:LISTEN and lsof -nP -iTCP:${config.httpPort} -sTCP:LISTEN, or ss -ltnp 'sport = :${config.frpPort}' and ss -ltnp 'sport = :${config.httpPort}'.`,
  )
}
