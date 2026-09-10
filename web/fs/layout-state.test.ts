import { describe, expect, it } from 'vitest'
import { navigationPanelWidth } from './layout-state'

describe('fs layout state', () => {
  it('uses a wide default and clamps persisted navigation widths', () => {
    expect(navigationPanelWidth(null)).toBe(400)
    expect(navigationPanelWidth('not-json')).toBe(400)
    expect(navigationPanelWidth('null')).toBe(400)
    expect(navigationPanelWidth('1e400')).toBe(400)
    expect(navigationPanelWidth('320')).toBe(320)
    expect(navigationPanelWidth('100')).toBe(180)
    expect(navigationPanelWidth('800')).toBe(560)
  })
})
