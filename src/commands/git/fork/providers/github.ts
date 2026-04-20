import type { Provider } from '../types'

export const githubProvider: Provider = {
  type: 'github',

  getArchiveUrl(host: string, owner: string, repo: string, ref: string): string {
    if (host === 'github.com') {
      return `https://api.github.com/repos/${owner}/${repo}/tarball/${ref}`
    }
    // GitHub Enterprise
    return `https://${host}/api/v3/repos/${owner}/${repo}/tarball/${ref}`
  },

  async getDefaultBranch(host: string, owner: string, repo: string, token?: string): Promise<string> {
    const apiBase = host === 'github.com' ? 'https://api.github.com' : `https://${host}/api/v3`
    const headers: Record<string, string> = { Accept: 'application/vnd.github.v3+json' }
    if (token)
      headers.Authorization = `Bearer ${token}`

    const res = await fetch(`${apiBase}/repos/${owner}/${repo}`, { headers })
    if (!res.ok)
      throw new Error(`Failed to get repo info: ${res.status} ${res.statusText}`)
    const data = await res.json() as { default_branch: string }
    return data.default_branch
  },

  buildCloneUrl(host: string, owner: string, repo: string, token?: string): string {
    if (token)
      return `https://${token}@${host}/${owner}/${repo}.git`
    return `https://${host}/${owner}/${repo}.git`
  },

  buildArchiveHeaders(token?: string): Record<string, string> {
    const headers: Record<string, string> = { Accept: 'application/vnd.github.v3+json' }
    if (token)
      headers.Authorization = `Bearer ${token}`
    return headers
  },
}
