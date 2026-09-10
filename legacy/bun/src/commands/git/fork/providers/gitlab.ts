import type { Provider } from '../types'

export const gitlabProvider: Provider = {
  type: 'gitlab',

  getArchiveUrl(baseUrl: string, owner: string, repo: string, ref: string): string {
    const projectPath = encodeURIComponent(`${owner}/${repo}`)
    return `${baseUrl}/api/v4/projects/${projectPath}/repository/archive.tar.gz?sha=${encodeURIComponent(ref)}`
  },

  async getDefaultBranch(baseUrl: string, owner: string, repo: string, token?: string): Promise<string> {
    const projectPath = encodeURIComponent(`${owner}/${repo}`)
    const headers: Record<string, string> = {}
    if (token)
      headers['PRIVATE-TOKEN'] = token

    const res = await fetch(`${baseUrl}/api/v4/projects/${projectPath}`, { headers })
    if (!res.ok)
      throw new Error(`Failed to get project info: ${res.status} ${res.statusText}`)
    const data = await res.json() as { default_branch: string }
    return data.default_branch
  },

  buildCloneUrl(baseUrl: string, owner: string, repo: string, token?: string): string {
    // Strip scheme to re-insert token credential
    const withoutScheme = baseUrl.replace(/^https?:\/\//, '')
    const scheme = baseUrl.startsWith('https') ? 'https' : 'http'
    if (token)
      return `${scheme}://oauth2:${token}@${withoutScheme}/${owner}/${repo}.git`
    return `${baseUrl}/${owner}/${repo}.git`
  },

  buildArchiveHeaders(token?: string): Record<string, string> {
    const headers: Record<string, string> = {}
    if (token)
      headers['PRIVATE-TOKEN'] = token
    return headers
  },
}
