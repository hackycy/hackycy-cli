export const DEFAULT_BACKOFF_MS = [1000, 2000, 4000, 8000, 15000, 30000] as const

export function backoffDelay(failureCount: number, schedule: readonly number[] = DEFAULT_BACKOFF_MS): number {
  return schedule[Math.min(Math.max(0, failureCount), schedule.length - 1)] ?? 30_000
}
