export interface GitLsOptions {
  days?: number
}

export interface CommitRecord {
  repo: string
  author: string
  date: string
  message: string
}
