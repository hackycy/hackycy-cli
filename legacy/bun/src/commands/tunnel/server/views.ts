import type { ClientRecord, ClientRuntimeState, TunnelDefinition, TunnelPresentationState } from '../types'
import type { AgentGateway } from './agent-gateway'
import type { TunnelControlPlane } from './control-plane'

export interface ClientView extends ClientRecord {
  runtime: ClientRuntimeState
  tunnelCounts: {
    total: number
    enabled: number
    applied: number
    pending: number
    error: number
  }
}

export function tunnelState(tunnel: TunnelDefinition, client: ClientRecord, runtime: ClientRuntimeState): TunnelPresentationState {
  if (!tunnel.enabled)
    return 'Disabled'
  if (runtime.lastError?.revision === client.desiredRevision)
    return 'Error'
  if (client.lastAppliedRevision !== client.desiredRevision || runtime.processState !== 'running')
    return 'Pending'
  return 'Applied'
}

export function clientView(controlPlane: TunnelControlPlane, gateway: AgentGateway, client: ClientRecord): ClientView {
  const runtime = gateway.state(client.id)
  const tunnels = controlPlane.listTunnels(client.id)
  return {
    ...client,
    runtime,
    tunnelCounts: {
      total: tunnels.length,
      enabled: tunnels.filter(tunnel => tunnel.enabled).length,
      applied: tunnels.filter(tunnel => tunnelState(tunnel, client, runtime) === 'Applied').length,
      pending: tunnels.filter(tunnel => tunnelState(tunnel, client, runtime) === 'Pending').length,
      error: tunnels.filter(tunnel => tunnelState(tunnel, client, runtime) === 'Error').length,
    },
  }
}
