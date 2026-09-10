export function deleteConfirmationTarget(paths: string[]): string {
  if (paths.length > 1)
    return `I CONFIRM DELETE ${paths.length} ITEMS`
  return paths[0]?.split('/').at(-1) ?? ''
}

export function matchesDeleteConfirmation(paths: string[], value: string): boolean {
  const target = deleteConfirmationTarget(paths)
  return target !== '' && value === target
}
