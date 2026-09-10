import { createRoot } from 'react-dom/client'
import { App } from './app'
import './styles.css'

const root = document.querySelector('#root')
if (!root)
  throw new Error('Missing application root')

createRoot(root).render(<App />)
