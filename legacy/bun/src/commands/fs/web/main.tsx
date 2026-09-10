import { WorkerPoolContextProvider } from '@pierre/diffs/react'
// @ts-expect-error Bun's file loader returns the emitted asset URL.
import diffWorkerUrl from '@pierre/diffs/worker/worker-portable.js' with { type: 'file' }
import { createRoot } from 'react-dom/client'
import { App } from './app'
import { configureMonacoWorkers } from './monaco-workers'
import 'react-photo-view/dist/react-photo-view.css'
import './styles.css'

function createCodePreviewWorker(): Worker {
  return new Worker(diffWorkerUrl, { type: 'module' })
}

const root = document.querySelector('#root')
if (!root)
  throw new Error('Missing application root')

configureMonacoWorkers()

createRoot(root).render(
  <WorkerPoolContextProvider poolOptions={{ poolSize: 1, workerFactory: createCodePreviewWorker }} highlighterOptions={{}}>
    <App />
  </WorkerPoolContextProvider>,
)
