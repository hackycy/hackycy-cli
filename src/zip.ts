import fs from 'node:fs/promises'
import path from 'node:path'
import { cancel, intro, outro, spinner } from '@clack/prompts'
import ansis from 'ansis'
import { zip as fflateZip } from 'fflate'
import revealFile from 'reveal-file'
import { printTitle } from './utils'

export interface ZipOptions {
  directory: string
  open?: boolean
  withDir?: boolean
}

type FflateFiles = Parameters<typeof fflateZip>[0]

function zipAsync(files: FflateFiles): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    fflateZip(files, { level: 6 }, (err, data) => {
      if (err)
        reject(err)
      else
        resolve(data)
    })
  })
}

async function collectAllFiles(
  dir: string,
  root: string,
  collected: Array<{ relative: string, absolute: string }>,
): Promise<void> {
  const entries = await fs.readdir(dir, { withFileTypes: true })
  for (const entry of entries) {
    const absolutePath = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      await collectAllFiles(absolutePath, root, collected)
    }
    else if (entry.isFile()) {
      collected.push({ relative: path.relative(root, absolutePath), absolute: absolutePath })
    }
  }
}

async function resolveZipDestination(dir: string): Promise<{ destination: string, file: string }> {
  let absDir = path.resolve(dir)
  const file = path.basename(absDir)

  // file dir has package.json, look node project
  try {
    // check if package.json exists in the directory
    const pkgPath = path.join(absDir, 'package.json')
    await fs.access(pkgPath)
    // if exists, check has "dist" folder, use it as zip source
    const distPath = path.join(absDir, 'dist')
    await fs.access(distPath)
    absDir = distPath
  }
  catch {
    // ignore, fallback to default behavior
  }

  return { destination: absDir, file }
}

export async function zip(options: ZipOptions): Promise<void> {
  printTitle()
  intro(ansis.bold('Zip Directory'))

  // Resolve and validate input directory
  const { destination: absDir, file } = await resolveZipDestination(options.directory)
  // Validate directory exists and is a directory
  try {
    const stat = await fs.stat(absDir)
    if (!stat.isDirectory()) {
      cancel(`Path is not a directory: ${ansis.dim(absDir)}`)
      return
    }
  }
  catch {
    cancel(`Directory not found: ${ansis.dim(absDir)}`)
    return
  }

  // Output zip is placed next to the original input directory: <dirname>.zip
  // Use origDir (not absDir) so dist-redirect doesn't affect the output location
  const outputPath = path.join(absDir, `${file}.zip`)

  // Phase 1: Collect files
  const collectSpin = spinner()
  collectSpin.start('Collecting files...')
  const fileEntries: Array<{ relative: string, absolute: string }> = []
  try {
    await collectAllFiles(absDir, absDir, fileEntries)
  }
  catch (err) {
    collectSpin.stop('File collection failed.')
    cancel(`Error reading directory: ${err instanceof Error ? err.message : String(err)}`)
    return
  }
  collectSpin.stop(`Collected ${fileEntries.length} file${fileEntries.length !== 1 ? 's' : ''}`)

  // Phase 2: Read files and compress
  const compressSpin = spinner()
  compressSpin.start('Compressing...')
  let zipData: Uint8Array
  try {
    const fflateFiles: FflateFiles = {}
    for (const entry of fileEntries) {
      // ZIP spec requires forward slashes; path.sep may be '\' on Windows
      const relKey = entry.relative.split(path.sep).join('/')
      // withDir: wrap files under <dirname>/ prefix; default is flat (no prefix)
      const zipKey = options.withDir ? `${file}/${relKey}` : relKey
      fflateFiles[zipKey] = await Bun.file(entry.absolute).bytes()
    }
    zipData = await zipAsync(fflateFiles)
  }
  catch (err) {
    compressSpin.stop('Compression failed.')
    cancel(`Compression error: ${err instanceof Error ? err.message : String(err)}`)
    return
  }
  compressSpin.stop('Compression complete')

  // Phase 3: Write zip file
  const writeSpin = spinner()
  writeSpin.start('Writing zip file...')
  try {
    await Bun.write(outputPath, zipData)
  }
  catch (err) {
    writeSpin.stop('Write failed.')
    cancel(`Failed to write zip: ${err instanceof Error ? err.message : String(err)}`)
    return
  }
  writeSpin.stop(`Saved ${ansis.cyan(outputPath)}`)

  // Reveal in Finder/Explorer by default; skip with --without-open
  if (options.open !== false) {
    try {
      await revealFile(outputPath)
    }
    catch {
      // Non-fatal: silently skip on headless/unsupported environments
    }
  }

  outro(ansis.green('Done!'))
}
