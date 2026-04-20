export interface CleanRule {
  id: string
  label: string
  category: string
  match: (entryName: string, parentDir: string) => boolean | Promise<boolean>
}

export interface CleanCandidate {
  rule: CleanRule
  path: string
}

export interface RmOptions {
  force?: boolean
  depth?: number
}
