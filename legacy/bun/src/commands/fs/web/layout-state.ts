export const NAVIGATION_PANEL_WIDTH_STORAGE_KEY = 'ycy-fs-navigation-width'
export const NAVIGATION_PANEL_DEFAULT_WIDTH = 400
export const NAVIGATION_PANEL_MIN_WIDTH = 180
export const NAVIGATION_PANEL_MAX_WIDTH = 560

export function navigationPanelWidth(stored: string | null): number {
  if (stored === null)
    return NAVIGATION_PANEL_DEFAULT_WIDTH
  try {
    const value = JSON.parse(stored)
    return typeof value === 'number' && Number.isFinite(value)
      ? Math.min(NAVIGATION_PANEL_MAX_WIDTH, Math.max(NAVIGATION_PANEL_MIN_WIDTH, value))
      : NAVIGATION_PANEL_DEFAULT_WIDTH
  }
  catch {
    return NAVIGATION_PANEL_DEFAULT_WIDTH
  }
}
