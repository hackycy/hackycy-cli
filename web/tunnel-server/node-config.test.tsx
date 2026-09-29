import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { Node404PageSource } from './node-config'

function renderMode(mode: 'inherit' | 'custom' | 'default', effectiveContent = ''): string {
  return renderToStaticMarkup(<Node404PageSource mode={mode} content="<main>Node</main>" effectiveContent={effectiveContent} onModeChange={() => {}} onContentChange={() => {}} />)
}

describe('node 404 page source', () => {
  it('shows the inherited Server state', () => {
    expect(renderMode('inherit', '<main>Server</main>')).toContain('Using the Server custom 404 page.')
    expect(renderMode('inherit')).toContain('FRP default is active.')
    expect(renderMode('inherit')).not.toContain('aria-label="Custom 404 page HTML"')
  })

  it('shows the editor only for a custom Node page', () => {
    expect(renderMode('custom')).toContain('aria-label="Custom 404 page HTML"')
    expect(renderMode('custom')).toContain('&lt;main&gt;Node&lt;/main&gt;')
    expect(renderMode('default')).not.toContain('aria-label="Custom 404 page HTML"')
  })
})
