import CssWorker from './workers/css.worker?worker'
import EditorWorker from './workers/editor.worker?worker'
import HtmlWorker from './workers/html.worker?worker'
import JsonWorker from './workers/json.worker?worker'
import TypescriptWorker from './workers/typescript.worker?worker'

interface MonacoWorkerEnvironment {
  getWorker: (moduleId: string, label: string) => Worker
}

export function configureMonacoWorkers(): void {
  const global = globalThis as typeof globalThis & { MonacoEnvironment?: MonacoWorkerEnvironment }
  global.MonacoEnvironment = {
    getWorker: (_moduleId, label) => {
      if (label === 'json')
        return new JsonWorker()
      if (label === 'css' || label === 'scss' || label === 'less')
        return new CssWorker()
      if (label === 'html' || label === 'handlebars' || label === 'razor')
        return new HtmlWorker()
      if (label === 'typescript' || label === 'javascript')
        return new TypescriptWorker()
      return new EditorWorker()
    },
  }
}
