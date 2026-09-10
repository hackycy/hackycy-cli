// Bun's file loader emits these prebundled Monaco workers as same-origin assets.
// The min/vs/assets files are self-contained, unlike Monaco's ESM worker entrypoints.
/* eslint-disable antfu/no-import-node-modules-by-path */
// @ts-expect-error Bun's file loader returns the emitted asset URL.
import cssWorkerUrl from '../../../../node_modules/monaco-editor/min/vs/assets/css.worker-URu8fCFR.js' with { type: 'file' }
// @ts-expect-error Bun's file loader returns the emitted asset URL.
import editorWorkerUrl from '../../../../node_modules/monaco-editor/min/vs/assets/editor.worker-lj3bdIIn.js' with { type: 'file' }
// @ts-expect-error Bun's file loader returns the emitted asset URL.
import htmlWorkerUrl from '../../../../node_modules/monaco-editor/min/vs/assets/html.worker-D1SL3iM8.js' with { type: 'file' }
// @ts-expect-error Bun's file loader returns the emitted asset URL.
import jsonWorkerUrl from '../../../../node_modules/monaco-editor/min/vs/assets/json.worker-CoJx_OPf.js' with { type: 'file' }
// @ts-expect-error Bun's file loader returns the emitted asset URL.
import typescriptWorkerUrl from '../../../../node_modules/monaco-editor/min/vs/assets/ts.worker-BWKtMYOk.js' with { type: 'file' }
/* eslint-enable antfu/no-import-node-modules-by-path */

interface MonacoWorkerEnvironment {
  getWorker: (moduleId: string, label: string) => Worker
}

export function configureMonacoWorkers(): void {
  const global = globalThis as typeof globalThis & { MonacoEnvironment?: MonacoWorkerEnvironment }
  global.MonacoEnvironment = {
    getWorker: (_moduleId, label) => {
      if (label === 'json')
        return new Worker(jsonWorkerUrl)
      if (label === 'css' || label === 'scss' || label === 'less')
        return new Worker(cssWorkerUrl)
      if (label === 'html' || label === 'handlebars' || label === 'razor')
        return new Worker(htmlWorkerUrl)
      if (label === 'typescript' || label === 'javascript')
        return new Worker(typescriptWorkerUrl)
      return new Worker(editorWorkerUrl)
    },
  }
}
