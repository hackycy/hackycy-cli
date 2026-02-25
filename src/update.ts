import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { intro, log, outro, spinner } from '@clack/prompts'
import ansis from 'ansis'
import { version as currentVersion } from '../package.json'

const REPO = 'hackycy-collection/hackycy-cli'
const API_URL = `https://api.github.com/repos/${REPO}/releases/latest`

function compareVersions(a: string, b: string): number {
  const pa = a.split('.').map(Number)
  const pb = b.split('.').map(Number)
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const na = pa[i] ?? 0
    const nb = pb[i] ?? 0
    if (na > nb)
      return 1
    if (na < nb)
      return -1
  }
  return 0
}

function getArtifactName(): string {
  const platform = process.platform
  const arch = process.arch

  const platformMap: Record<string, string> = {
    darwin: 'macos',
    linux: 'linux',
    win32: 'windows',
  }

  const osName = platformMap[platform]
  if (!osName)
    throw new Error(`Unsupported platform: ${platform}`)
  if (arch !== 'x64' && arch !== 'arm64')
    throw new Error(`Unsupported architecture: ${arch}`)

  const name = `ycy-${osName}-${arch}`
  return platform === 'win32' ? `${name}.exe` : name
}

async function replaceBinary(tempFile: string, targetPath: string): Promise<void> {
  const backupPath = `${targetPath}.backup`

  try {
    // Remove any existing backup
    if (fs.existsSync(backupPath)) {
      fs.unlinkSync(backupPath)
    }

    // Set executable permission on Unix
    if (process.platform !== 'win32') {
      fs.chmodSync(tempFile, 0o755)
    }

    // Backup current binary
    fs.renameSync(targetPath, backupPath)

    try {
      // Move new binary into place
      fs.renameSync(tempFile, targetPath)
    }
    catch (err: unknown) {
      // Cross-device rename fallback
      if ((err as NodeJS.ErrnoException).code === 'EXDEV') {
        fs.copyFileSync(tempFile, targetPath)
        fs.unlinkSync(tempFile)
      }
      else {
        throw err
      }
    }

    // Remove backup
    fs.unlinkSync(backupPath)
  }
  catch (err) {
    // Restore backup if something went wrong
    if (fs.existsSync(backupPath) && !fs.existsSync(targetPath)) {
      fs.renameSync(backupPath, targetPath)
    }
    // Clean up temp file
    if (fs.existsSync(tempFile)) {
      fs.unlinkSync(tempFile)
    }
    throw err
  }
}

export async function updateCli(): Promise<void> {
  intro(ansis.bold('Update ycy CLI'))

  const spin = spinner()

  try {
    // 1. Check latest version
    spin.start('Checking for updates...')

    const response = await fetch(API_URL, {
      headers: { Accept: 'application/vnd.github.v3+json' },
    })

    if (!response.ok) {
      spin.stop('Check failed.')
      if (response.status === 403) {
        log.error('GitHub API rate limit exceeded. Please try again later.')
      }
      else {
        log.error(`Failed to check for updates: HTTP ${response.status}`)
      }
      outro('Update aborted.')
      return
    }

    const release = await response.json() as { tag_name: string }
    const latestVersion = release.tag_name.replace(/^v/, '')

    // 2. Compare versions
    if (compareVersions(currentVersion, latestVersion) >= 0) {
      spin.stop('Already up to date!')
      log.success(`Current version ${ansis.cyan(`v${currentVersion}`)} is the latest.`)
      outro('No update needed.')
      return
    }

    spin.stop(`New version available: ${ansis.cyan(`v${currentVersion}`)} → ${ansis.green(`v${latestVersion}`)}`)

    // 3. Download new binary
    const downloadSpin = spinner()
    downloadSpin.start(`Downloading v${latestVersion}...`)

    const artifactName = getArtifactName()
    const downloadUrl = `https://github.com/${REPO}/releases/download/v${latestVersion}/${artifactName}`

    const downloadResponse = await fetch(downloadUrl)
    if (!downloadResponse.ok) {
      downloadSpin.stop('Download failed.')
      log.error(`Failed to download: HTTP ${downloadResponse.status}`)
      outro('Update aborted.')
      return
    }

    const arrayBuffer = await downloadResponse.arrayBuffer()

    if (arrayBuffer.byteLength === 0) {
      downloadSpin.stop('Download failed.')
      log.error('Downloaded file is empty.')
      outro('Update aborted.')
      return
    }

    // Write to temp file
    const tempFile = path.join(os.tmpdir(), `ycy-update-${Date.now()}`)
    await Bun.write(tempFile, arrayBuffer)

    downloadSpin.stop('Download complete!')

    // 4. Replace current binary
    const replaceSpin = spinner()
    replaceSpin.start('Installing update...')

    const currentExePath = process.execPath
    await replaceBinary(tempFile, currentExePath)

    replaceSpin.stop('Installation complete!')

    log.success(`Updated ycy to ${ansis.green(`v${latestVersion}`)}`)
    outro('Restart your terminal to use the new version.')
  }
  catch (error) {
    log.error(`Update failed: ${error instanceof Error ? error.message : String(error)}`)
    outro('Update aborted.')
    process.exit(1)
  }
}
