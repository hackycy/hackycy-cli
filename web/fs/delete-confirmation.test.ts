import { describe, expect, it } from 'vitest'
import { deleteConfirmationTarget, matchesDeleteConfirmation } from './delete-confirmation'

describe('delete confirmation', () => {
  it('uses the item name for a single deletion', () => {
    expect(deleteConfirmationTarget(['docs/report.txt'])).toBe('report.txt')
  })

  it('binds multi-item confirmation text to the selected item count', () => {
    expect(deleteConfirmationTarget(['first.txt', 'second.txt'])).toBe('I CONFIRM DELETE 2 ITEMS')
    expect(deleteConfirmationTarget(['first.txt', 'second.txt', 'third.txt'])).toBe('I CONFIRM DELETE 3 ITEMS')
    expect(deleteConfirmationTarget(['1.txt', '2.txt', '3.txt', '4.txt', '5.txt', '6.txt', '7.txt'])).toBe('I CONFIRM DELETE 7 ITEMS')
  })

  it('requires an exact confirmation text', () => {
    expect(matchesDeleteConfirmation(['first.txt', 'second.txt'], 'I CONFIRM DELETE 2 ITEMS')).toBe(true)
    expect(matchesDeleteConfirmation(['first.txt', 'second.txt'], 'I CONFIRM DELETE 3 ITEMS')).toBe(false)
    expect(matchesDeleteConfirmation(['first.txt', 'second.txt'], 'i confirm delete 2 items')).toBe(false)
    expect(matchesDeleteConfirmation(['docs/report.txt'], 'report.txt')).toBe(true)
    expect(matchesDeleteConfirmation(['docs/report.txt'], 'docs/report.txt')).toBe(false)
  })
})
