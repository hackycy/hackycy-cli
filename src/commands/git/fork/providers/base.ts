import type { Provider } from '../types'
import { githubProvider } from './github'
import { gitlabProvider } from './gitlab'

export { githubProvider } from './github'
export { gitlabProvider } from './gitlab'

export function getProvider(type: 'github' | 'gitlab'): Provider {
  return type === 'github' ? githubProvider : gitlabProvider
}
