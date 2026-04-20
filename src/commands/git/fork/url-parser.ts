import { getInstanceByHost, getInstanceByName } from './config'

interface ResolvedRepo {
  host: string
  scheme: string
  owner: string
  repo: string
  ref?: string
  instanceName?: string
  providerType: 'github' | 'gitlab'
  token?: string
}

/**
 * Parse repo input into structured data.
 *
 * Supported formats:
 *   1. https://github.com/owner/repo#ref
 *   2. http://172.16.8.239:23081/owner/repo#ref
 *   3. github.com/owner/repo#ref
 *   4. alias:owner/repo#ref
 *   5. owner/repo#ref  (defaults to github.com)
 */
export async function parseRepoUrl(input: string): Promise<ResolvedRepo> {
  // Extract ref (after #)
  let ref: string | undefined
  let rest = input
  const hashIdx = rest.indexOf('#')
  if (hashIdx !== -1) {
    ref = rest.slice(hashIdx + 1)
    rest = rest.slice(0, hashIdx)
  }

  let host: string
  let scheme: string
  let ownerRepo: { owner: string, repo: string }
  let instanceName: string | undefined

  // Check if it's a full URL (has ://)
  if (rest.includes('://')) {
    const url = new URL(rest)
    host = url.host // includes port (e.g. 172.16.8.239:23081)
    scheme = url.protocol.slice(0, -1) // strip trailing ':'
    const pathStr = url.pathname.replace(/^\//, '').replace(/\.git$/, '')
    ownerRepo = splitOwnerRepo(pathStr, input)
  }
  // Check if it's alias:owner/repo format (colon not followed by //)
  else if (rest.includes(':')) {
    const colonIdx = rest.indexOf(':')
    const alias = rest.slice(0, colonIdx)
    const pathPart = rest.slice(colonIdx + 1)
    const instance = await getInstanceByName(alias)
    if (!instance)
      throw new Error(`Unknown instance alias: "${alias}". Run "ycy git config add" to configure it.`)

    instanceName = alias
    host = instance.host
    scheme = instance.scheme ?? 'https'
    ownerRepo = splitOwnerRepo(pathPart.replace(/\.git$/, ''), input)

    return {
      host,
      scheme,
      ...ownerRepo,
      ref,
      instanceName,
      providerType: instance.type,
      token: instance.decryptedToken,
    }
  }
  // Check if it has a dot (host/owner/repo)
  else if (rest.includes('/') && rest.split('/')[0]!.includes('.')) {
    const firstSlash = rest.indexOf('/')
    host = rest.slice(0, firstSlash)
    scheme = 'https'
    const pathStr = rest.slice(firstSlash + 1).replace(/\.git$/, '')
    ownerRepo = splitOwnerRepo(pathStr, input)
  }
  // Default: owner/repo on github.com
  else {
    host = 'github.com'
    scheme = 'https'
    ownerRepo = splitOwnerRepo(rest.replace(/\.git$/, ''), input)
  }

  // Determine provider type and token from config (lookup by host)
  const instanceByHost = await getInstanceByHost(host)
  if (instanceByHost) {
    return {
      host,
      scheme: instanceByHost.instance.scheme ?? scheme,
      ...ownerRepo,
      ref,
      instanceName: instanceByHost.name,
      providerType: instanceByHost.instance.type,
      token: instanceByHost.decryptedToken,
    }
  }

  // Auto-detect provider type by host
  const providerType = detectProviderType(host)

  return { host, scheme, ...ownerRepo, ref, instanceName, providerType }
}

function splitOwnerRepo(pathStr: string, originalInput: string): { owner: string, repo: string } {
  const parts = pathStr.split('/')
  if (parts.length < 2)
    throw new Error(`Invalid repository path: ${originalInput}. Expected format: owner/repo`)
  const repo = parts.pop()!
  const owner = parts.join('/')
  return { owner, repo }
}

function detectProviderType(host: string): 'github' | 'gitlab' {
  if (host === 'github.com' || host.includes('github'))
    return 'github'
  if (host === 'gitlab.com' || host.includes('gitlab'))
    return 'gitlab'
  throw new Error(`Cannot determine provider type for host "${host}". Run "ycy git config add" to configure it.`)
}
