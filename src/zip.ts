import fs from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import { cancel, intro, isCancel, multiselect, outro, select, spinner, text } from '@clack/prompts'
import ansis from 'ansis'
import { zipSync as fflateZipSync } from 'fflate'
import revealFile from 'reveal-file'
import { printTitle } from './utils'

const DEFAULT_GLOB_PATTERN = '**/*'

export interface ZipOptions {
  directory: string
  open?: boolean
  withDir?: string
}

type FflateFiles = Parameters<typeof fflateZipSync>[0]

function isUint8ArrayLike(value: unknown): value is Uint8Array {
  return value instanceof Uint8Array
}

async function collectAllFiles(
  dir: string,
  patterns: string | string[] = '**/*',
): Promise<Array<{ relative: string, absolute: string }>> {
  const patternList = Array.isArray(patterns) ? patterns : [patterns]
  const seen = new Set<string>()
  const collected: Array<{ relative: string, absolute: string }> = []

  for (const pattern of patternList) {
    const glob = new Bun.Glob(pattern)
    for await (const file of glob.scan({ cwd: dir, onlyFiles: true })) {
      const absolute = path.join(dir, file)
      let stat
      try {
        stat = await fs.lstat(absolute)
      }
      catch {
        continue
      }

      if (!stat.isFile())
        continue

      if (!seen.has(absolute)) {
        seen.add(absolute)
        collected.push({ relative: file, absolute })
      }
    }
  }

  return collected
}

async function resolveZipOptions(dir: string): Promise<{ input: string, file: string, glob: string[] }> {
  const originalDir = path.resolve(dir)
  let absDir = originalDir

  let glob: string[] = [DEFAULT_GLOB_PATTERN]

  const selectInputDirs: string[] = [absDir]

  let isNodeProject = false

  // file dir has package.json, look node project
  try {
    // check if package.json exists in the directory
    const pkgPath = path.join(absDir, 'package.json')
    await fs.access(pkgPath)
    isNodeProject = true

    // if exists, check has "dist" folder, use it as zip source
    const distPath = path.join(absDir, 'dist')
    const distStat = await fs.stat(distPath)
    if (distStat.isDirectory()) {
      absDir = distPath
      selectInputDirs.unshift(absDir)
    }
  }
  catch {
    // ignore, fallback to default behavior
  }

  if (selectInputDirs.length > 1) {
    const selectedDir = await select({
      message: 'Multiple directories found. Select the one to zip:',
      options: selectInputDirs.map(dir => ({ value: dir, label: path.relative(process.cwd(), dir) || '.' })),
    })

    if (isCancel(selectedDir)) {
      cancel('Operation cancelled.')
      process.exit(0)
    }

    absDir = path.resolve(selectedDir)
  }

  // multiselect for glob patterns, default is **/*
  const selectedPatterns = await multiselect({
    message: 'Select file patterns to include in the zip:',
    options: [
      { value: DEFAULT_GLOB_PATTERN, label: 'All files (default)' },
      { value: '*.html', label: 'HTML files' },
    ],
    initialValues: [DEFAULT_GLOB_PATTERN],
  })

  if (isCancel(selectedPatterns)) {
    cancel('Operation cancelled.')
    process.exit(0)
  }

  if (!selectedPatterns.includes(DEFAULT_GLOB_PATTERN)) {
    glob = selectedPatterns.length > 0 ? selectedPatterns : [DEFAULT_GLOB_PATTERN]
  }

  // default zip file name is the directory name, if withDir option is not set
  let file = path.basename(originalDir)
  if (!isNodeProject) {
    file = path.basename(absDir)
  }

  // prompt output zip file name, default is the directory name
  const fileOutput = await text({
    message: 'Enter the name for the zip file (without .zip extension):',
    initialValue: file,
    validate(value) {
      return !value || value.trim() === '' ? 'File name cannot be empty' : undefined
    },
  })

  if (isCancel(fileOutput)) {
    cancel('Operation cancelled.')
    process.exit(0)
  }

  file = fileOutput.trim()

  return { input: absDir, file, glob }
}

export async function zip(options: ZipOptions): Promise<void> {
  printTitle()
  intro(ansis.bold('Zip Directory'))

  // Resolve and validate input directory
  const { input: absDir, file, glob } = await resolveZipOptions(options.directory)
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

  // Output zip is placed in the resolved input directory: <dirname>.zip
  const outputPath = path.join(absDir, `${file}.zip`)

  // Phase 1: Collect files
  const collectSpin = spinner()
  collectSpin.start('Collecting files...')
  let fileEntries: Array<{ relative: string, absolute: string }> = []
  try {
    fileEntries = await collectAllFiles(absDir, glob)
  }
  catch (err) {
    collectSpin.stop('File collection failed.')
    cancel(`Error reading directory: ${err instanceof Error ? err.message : String(err)}`)
    return
  }

  if (fileEntries.length === 0) {
    collectSpin.stop('No files found to zip.')
    cancel('No files matched the selected patterns.')
    return
  }

  collectSpin.stop(`Collected ${fileEntries.length} file${fileEntries.length !== 1 ? 's' : ''}`)

  // Phase 2: Read files and compress
  const compressSpin = spinner()
  compressSpin.start('Compressing...')
  let zipData: Uint8Array
  try {
    const fflateFiles: FflateFiles = Object.create(null) as FflateFiles
    let includedCount = 0
    for (const entry of fileEntries) {
      // ZIP spec requires forward slashes; path.sep may be '\' on Windows
      if (entry.absolute === outputPath)
        continue

      const relKey = entry.relative.split(path.sep).join('/')
      // withDir: wrap files under <dirname>/ prefix; default is flat (no prefix)
      const zipKey = options.withDir ? `${options.withDir}/${relKey}` : relKey

      const fileBuffer = await Bun.file(entry.absolute).arrayBuffer()
      const fileBytes = new Uint8Array(fileBuffer)

      if (!isUint8ArrayLike(fileBytes)) {
        throw new Error(`Invalid file bytes for: ${entry.relative}`)
      }

      fflateFiles[zipKey] = fileBytes
      includedCount += 1
    }

    if (includedCount === 0) {
      compressSpin.stop('No files available to compress.')
      cancel('No valid files matched after filtering.')
      return
    }

    zipData = fflateZipSync(fflateFiles, { level: 6 })
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
