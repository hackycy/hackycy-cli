import type { AccountKind, AccountRole, ClientRecord, ClientRuntimeState, PublicTunnelDefinition, TunnelPresentationState } from './domain'

export interface CurrentAccount {
  id: string
  kind: AccountKind
  username: string
  role: AccountRole
  createdAt: string
  updatedAt: string
  managedByEnvironment: boolean
}

export interface AccountView extends CurrentAccount {
  clientCount: number
}

export interface ClientView extends Omit<ClientRecord, 'ownerAccountId'> {
  owner: { id: string, username: string }
  runtime: ClientRuntimeState
  tunnelCounts: { total: number, enabled: number, applied: number, pending: number, error: number }
}

export type TunnelView = PublicTunnelDefinition & { state: TunnelPresentationState }

export interface TunnelImportCandidate {
  id: string
  label: string
  protocol: 'http' | 'tcp' | 'udp'
  customDomains?: string[]
  location?: string | null
  serverPort?: number
  localHost: string
  localPort: number
  basicAuth: { username: string, passwordConfigured: true } | null
}

export interface TunnelImportPreview {
  candidates: TunnelImportCandidate[]
  ignored: Array<{ proxy?: string, reason: string }>
}

export class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message)
  }
}

export async function apiJson<T>(url: string, init?: RequestInit): Promise<T> {
  let response: Response
  try {
    response = await fetch(url, init)
  }
  catch (cause) {
    if (cause instanceof DOMException && cause.name === 'AbortError')
      throw cause
    throw new ApiError(0, 'Unable to reach the Tunnel Control Plane')
  }
  let body: any
  if (response.status !== 204) {
    const text = await response.text()
    try {
      body = text ? JSON.parse(text) : undefined
    }
    catch {
      body = undefined
    }
  }
  if (!response.ok && response.status === 401)
    window.dispatchEvent(new Event('tunnel-authentication-required'))
  if (!response.ok)
    throw new ApiError(response.status, body?.error?.message ?? `Request failed (${response.status})`)
  return body as T
}

export function jsonRequest(method: string, body?: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  }
}
