import type { ZipPlan, ZipPlanningAnswer, ZipPlanningStep } from './engine'
import type { ZipOptions } from './types'
import fs from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import { cancel, intro, isCancel, multiselect, note, outro, select, spinner, text } from '@clack/prompts'
import ansis from 'ansis'
import revealFile from 'reveal-file'
import { printTitle } from '../../shared/utils'
import { buildZipData, collectArchiveFiles, writeZipFile } from './archive'
import {
  applyZipPlanningAnswer,
  createZipPlanningSession,
  resolveZipPlanningStep,
} from './engine'

async function promptForStep(step: ZipPlanningStep): Promise<ZipPlanningAnswer> {
  if ('note' in step && step.note)
    note(step.note.lines.join('\n'), step.note.title)

  switch (step.type) {
    case 'select-package': {
      const selectedPackage = await select({
        message: step.message,
        options: step.options,
      })

      if (isCancel(selectedPackage)) {
        cancel('Operation cancelled.')
        process.exit(0)
      }

      return { type: 'package-root', value: selectedPackage }
    }

    case 'select-source': {
      const selectedSource = await select({
        message: step.message,
        options: step.options,
      })

      if (isCancel(selectedSource)) {
        cancel('Operation cancelled.')
        process.exit(0)
      }

      return { type: 'source-directory', value: selectedSource }
    }

    case 'select-glob': {
      const selectedPatterns = await multiselect({
        message: step.message,
        options: step.options,
        initialValues: step.initialValues,
      })

      if (isCancel(selectedPatterns)) {
        cancel('Operation cancelled.')
        process.exit(0)
      }

      return { type: 'glob-patterns', value: selectedPatterns }
    }

    case 'edit-output-file': {
      const fileOutput = await text({
        message: step.message,
        initialValue: step.initialValue,
        validate(value) {
          return !value || value.trim() === '' ? 'File name cannot be empty' : undefined
        },
      })

      if (isCancel(fileOutput)) {
        cancel('Operation cancelled.')
        process.exit(0)
      }

      return { type: 'output-file', value: fileOutput }
    }

    case 'complete':
      return { type: 'output-file', value: step.plan.file }
  }
}

async function resolveZipPlan(directory: string): Promise<ZipPlan> {
  let session = await createZipPlanningSession(directory)

  while (true) {
    const resolved = await resolveZipPlanningStep(session)
    session = resolved.session

    if (resolved.step.type === 'complete') {
      note(resolved.step.note.lines.join('\n'), resolved.step.note.title)
      return resolved.step.plan
    }

    const answer = await promptForStep(resolved.step)
    session = applyZipPlanningAnswer(session, answer)
  }
}

export async function zip(options: ZipOptions): Promise<void> {
  printTitle()
  intro(ansis.bold('Zip Directory'))

  const plan = await resolveZipPlan(options.directory)
  const absDir = plan.input

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

  const outputPath = path.join(absDir, `${plan.file}.zip`)

  const collectSpin = spinner()
  collectSpin.start('Collecting files...')
  let fileEntries = []
  try {
    fileEntries = await collectArchiveFiles(absDir, plan.glob)
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

  const compressSpin = spinner()
  compressSpin.start('Compressing...')
  let zipData: Uint8Array
  let includedCount = 0
  try {
    const archive = await buildZipData(fileEntries, outputPath, options.withDir)
    zipData = archive.zipData
    includedCount = archive.includedCount
  }
  catch (err) {
    const message = err instanceof Error ? err.message : String(err)
    if (message === 'No valid files matched after filtering.')
      compressSpin.stop('No files available to compress.')
    else
      compressSpin.stop('Compression failed.')

    cancel(message)
    return
  }
  compressSpin.stop(`Compression complete (${includedCount} file${includedCount === 1 ? '' : 's'})`)

  const writeSpin = spinner()
  writeSpin.start('Writing zip file...')
  try {
    await writeZipFile(outputPath, zipData)
  }
  catch (err) {
    writeSpin.stop('Write failed.')
    cancel(`Failed to write zip: ${err instanceof Error ? err.message : String(err)}`)
    return
  }
  writeSpin.stop(`Saved ${ansis.cyan(outputPath)}`)

  if (options.open !== false) {
    try {
      await revealFile(outputPath)
    }
    catch {
      // Non-fatal: silently skip on headless or unsupported environments.
    }
  }

  outro(ansis.green('Done!'))
}
