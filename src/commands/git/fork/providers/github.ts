import type { Provider } from '../types'

export const githubProvider: Provider = {
  type: 'github',

  getArchiveUrl(baseUrl: string, owner: string, repo: string, ref: string): string {
    if (baseUrl === 'https://github.com') {
      return `https://api.github.com/repos/${owner}/${repo}/tarball/${ref}`
    }
    // GitHub Enterprise
    return `${baseUrl}/api/v3/repos/${owner}/${repo}/tarball/${ref}`
  },

  async getDefaultBranch(baseUrl: string, owner: string, repo: string, token?: string): Promise<string> {
    const apiBase = baseUrl === 'https://github.com'
      ? 'https://api.github.com'
      : `${baseUrl}/api/v3`
    const headers: Record<string, string> = { Accept: 'application/vnd.github.v3+json' }
    if (token)
      headers.Authorization = `Bearer ${token}`

    const res = await fetch(`${apiBase}/repos/${owner}/${repo}`, { headers })
    if (!res.ok)
      throw new Error(`Failed to get repo info: ${res.status} ${res.statusText}`)
    const data = await res.json() as { default_branch: string }
    return data.default_branch
  },

  buildCloneUrl(baseUrl: string, owner: string, repo: string, token?: string): string {
    const withoutScheme = baseUrl.replace(/^https?:\/\//, '')
    const scheme = baseUrl.startsWith('https') ? 'https' : 'http'
    if (token)
      return `${scheme}://${token}@${withoutScheme}/${owner}/${repo}.git`
    return `${baseUrl}/${owner}/${repo}.git`
  },

  buildArchiveHeaders(token?: string): Record<string, string> {
    const headers: Record<string, string> = { Accept: 'application/vnd.github.v3+json' }
    if (token)
      headers.Authorization = `Bearer ${token}`
    return headers
  },
}
