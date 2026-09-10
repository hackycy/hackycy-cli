import { FileSessionManager } from '../../shared/file-session'

const PASSWORD_ALGORITHM = { algorithm: 'argon2id', memoryCost: 65_536, timeCost: 3 } as const

interface FsAccount {
  username: string
  key: string
  passwordHash: string
  sessionRevision: string
}

export interface FsSession {
  account: { username: string }
  expiresAt: string
}

export interface FsSessionGrant extends FsSession {
  token: string
}

export interface FsAuthenticationOptions {
  directory: string
  sessionLifetimeMs?: number
  maxAccountSessions?: number
  maxSessions?: number
}

function parseAccount(specification: string): { username: string, key: string, password: string } {
  const separator = specification.indexOf(':')
  if (separator === -1)
    throw new Error('Account must use <username>:<password>')

  const username = specification.slice(0, separator)
  if (!/^[\w.-]{1,64}$/.test(username))
    throw new Error('Username must contain 1-64 ASCII letters, numbers, dots, underscores, or hyphens')

  const password = specification.slice(separator + 1)
  if (password.length < 5 || password.length > 256)
    throw new Error('Password must contain 5-256 characters')

  return { username, key: username.toLowerCase(), password }
}

function usernameKey(value: string): string | undefined {
  return /^[\w.-]{1,64}$/.test(value) ? value.toLowerCase() : undefined
}

export class FsAuthentication {
  private closed = false

  private constructor(
    private readonly accounts: Map<string, FsAccount>,
    private readonly sessions: FileSessionManager,
  ) {}

  static async create(specifications: string[], options: FsAuthenticationOptions): Promise<FsAuthentication | undefined> {
    if (specifications.length === 0)
      return undefined

    const parsedAccounts = specifications.map(parseAccount)
    const keys = new Set<string>()
    for (const account of parsedAccounts) {
      if (keys.has(account.key))
        throw new Error(`Username '${account.username}' is specified more than once`)
      keys.add(account.key)
    }

    const sessions = await FileSessionManager.open({
      directory: options.directory,
      idleLifetimeMs: options.sessionLifetimeMs,
      maxSubjectSessions: options.maxAccountSessions,
      maxSessions: options.maxSessions,
    })
    try {
      const accounts = new Map<string, FsAccount>()
      for (const account of parsedAccounts) {
        accounts.set(account.key, {
          username: account.username,
          key: account.key,
          passwordHash: await Bun.password.hash(account.password, PASSWORD_ALGORITHM),
          sessionRevision: sessions.credentialRevision(`${account.key}\0${account.password}`),
        })
      }
      return new FsAuthentication(accounts, sessions)
    }
    catch (cause) {
      sessions.close()
      throw cause
    }
  }

  get accountCount(): number {
    return this.accounts.size
  }

  get sessionDirectory(): string {
    return this.sessions.directory
  }

  private createSession(account: FsAccount): FsSessionGrant {
    const session = this.sessions.issue(account.key, account.sessionRevision)
    return { token: session.token, expiresAt: session.expiresAt, account: { username: account.username } }
  }

  async signIn(input: { username: string, password: string }): Promise<FsSessionGrant | undefined> {
    if (this.closed)
      return undefined
    const account = this.accounts.get(usernameKey(input.username) ?? '')
    const fallbackHash = this.accounts.values().next().value!.passwordHash
    const verified = await Bun.password.verify(input.password, account?.passwordHash ?? fallbackHash)
    return account && verified ? this.createSession(account) : undefined
  }

  resume(token: string | undefined): FsSession | undefined {
    if (this.closed || !token)
      return undefined
    const session = this.sessions.resume(token, subject => this.accounts.get(subject)?.sessionRevision)
    const account = session ? this.accounts.get(session.subject) : undefined
    if (!session || !account)
      return undefined
    return { expiresAt: session.expiresAt, account: { username: account.username } }
  }

  signOut(token: string | undefined): void {
    this.sessions.revoke(token)
  }

  observe(token: string, listener: () => void): () => void {
    return this.sessions.observe(token, listener)
  }

  close(): void {
    if (this.closed)
      return
    this.closed = true
    this.sessions.close()
  }
}

export const createFsAuthentication = FsAuthentication.create
