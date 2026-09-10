export interface CleanAction {
  id: string
  label: string
  scan: (cwd: string, depth: number) => Promise<string[]>
}

export interface RmOptions {
  force?: boolean
  depth?: number
}
