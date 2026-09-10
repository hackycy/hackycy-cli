import { createRoot } from 'react-dom/client'
import { App } from './app'
import { FeedbackProvider } from './ui'
import '../shared/admin/styles.css'
import './styles.css'

const root = document.querySelector('#root')
if (!root)
  throw new Error('Missing application root')

createRoot(root).render(<FeedbackProvider><App /></FeedbackProvider>)
