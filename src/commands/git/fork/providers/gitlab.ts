import type { Provider } from '../types'

export const gitlabProvider: Provider = {
  type: 'gitlab',

  getArchiveUrl(host: string, owner: string, repo: string, ref: string): string {
    const projectPath = encodeURIComponent(`${owner}/${repo}`)
    return `https://${host}/api/v4/projects/${projectPath}/repository/archive.tar.gz?sha=${encodeURIComponent(ref)}`
  },

  async getDefaultBranch(host: string, owner: string, repo: string, token?: string): Promise<string> {
    const projectPath = encodeURIComponent(`${owner}/${repo}`)
    const headers: Record<string, string> = {}
    if (token)
      headers['PRIVATE-TOKEN'] = token

    const res = await fetch(`https://${host}/api/v4/projects/${projectPath}`, { headers })
    if (!res.ok)
      throw new Error(`Failed to get project info: ${res.status} ${res.statusText}`)
    const data = await res.json() as { default_branch: string }
    return data.default_branch
  },

  buildCloneUrl(host: string, owner: string, repo: string, token?: string): string {
    if (token)
      return `https://oauth2:${token}@${host}/${owner}/${repo}.git`
    return `https://${host}/${owner}/${repo}.git`
  },

  buildArchiveHeaders(token?: string): Record<string, string> {
    const headers: Record<string, string> = {}
    if (token)
      headers['PRIVATE-TOKEN'] = token
    return headers
  },
}
