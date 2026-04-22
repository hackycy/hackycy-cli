export type PackageManager = 'npm' | 'yarn' | 'pnpm' | 'bun'

export interface RunOptions {
  passthroughArgs?: string[]
}
