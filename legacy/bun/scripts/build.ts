import path from 'node:path'
import process from 'node:process'
import tailwind from 'bun-plugin-tailwind'
import { prepareSevenZipRuntime } from './prepare-seven-zip'

interface BuildArguments {
  outfile: string
  target?: Bun.Build.CompileTarget
}

function readArgument(name: string): string | undefined {
  const index = process.argv.indexOf(name)
  return index === -1 ? undefined : process.argv[index + 1]
}

function parseArguments(): BuildArguments {
  const target = readArgument('--target')
  const outfile = readArgument('--outfile') ?? 'ycy'

  return {
    outfile: path.resolve(outfile),
    target: target as Bun.Build.CompileTarget | undefined,
  }
}

const options = parseArguments()
const sevenZipEntrypoints = await prepareSevenZipRuntime(options.target)
const result = await Bun.build({
  entrypoints: [
    path.resolve('src/cli.ts'),
    path.resolve('src/commands/fs/thumbnail-worker.ts'),
    ...sevenZipEntrypoints,
  ],
  naming: {
    entry: '[dir]/[name]-[hash].[ext]',
  },
  minify: true,
  plugins: [tailwind],
  root: path.resolve('.'),
  sourcemap: 'external',
  target: 'bun',
  compile: {
    ...(options.target ? { target: options.target } : {}),
    outfile: options.outfile,
    autoloadDotenv: false,
    autoloadBunfig: false,
  },
})

if (!result.success) {
  for (const log of result.logs)
    console.error(log)
  process.exit(1)
}

console.log(`Built ${options.outfile}`)
