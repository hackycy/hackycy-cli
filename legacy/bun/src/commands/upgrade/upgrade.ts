import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { log, outro, spinner } from '@clack/prompts'
import ansis from 'ansis'
import { version as currentVersion } from '../../../package.json'
import { printTitle } from '../../shared/utils'
import { createUpdateTransaction, getInternalUpdateArgs, getUpdateStatePath, readUpdateState, writeUpdateState } from './updater'

const REPO = 'hackycy/hackycy-cli'
const API_URL = `https://api.github.com/repos/${REPO}/releases/latest`
const CHECKSUMS_FILE = 'SHA256SUMS'

function compareVersions(a: string, b: string): number {
  return Bun.semver.order(a, b)
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

function clearQuarantine(filePath: string): void {
  if (process.platform !== 'darwin') {
    return
  }

  const result = Bun.spawnSync(['xattr', '-d', 'com.apple.quarantine', filePath], {
    stdout: 'ignore',
    stderr: 'ignore',
  })

  if (result.exitCode !== 0 && result.exitCode !== 1) {
    throw new Error('Failed to clear macOS quarantine attribute.')
  }
}

function parseChecksumsFile(content: string): Map<string, string> {
  const checksums = new Map<string, string>()

  for (const line of content.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed) {
      continue
    }

    const hash = trimmed.slice(0, 64)
    const fileName = trimmed.slice(64).trimStart().replace(/^\*/, '').trim()

    if (!/^[a-f0-9]{64}$/i.test(hash) || !fileName) {
      continue
    }

    checksums.set(fileName, hash.toLowerCase())
  }

  return checksums
}

async function sha256Hex(data: ArrayBuffer | Uint8Array): Promise<string> {
  const input = Uint8Array.from(data instanceof Uint8Array ? data : new Uint8Array(data))
  const digest = await crypto.subtle.digest('SHA-256', input)
  return Array.from(new Uint8Array(digest))
    .map(byte => byte.toString(16).padStart(2, '0'))
    .join('')
}

function decodeOutput(output: Uint8Array<ArrayBufferLike> | undefined): string {
  if (!output) {
    return ''
  }

  return new TextDecoder().decode(output).trim()
}

function isExpectedVersionOutput(actualVersion: string, expectedVersion: string): boolean {
  if (!actualVersion) {
    return false
  }

  return actualVersion === expectedVersion || actualVersion.startsWith(`ycy/${expectedVersion}`)
}

function verifyBinaryExecutable(filePath: string, expectedVersion: string): void {
  const result = Bun.spawnSync([filePath, '--version'], {
    stdout: 'pipe',
    stderr: 'pipe',
  })

  if (result.exitCode !== 0) {
    const stderr = decodeOutput(result.stderr)
    throw new Error(stderr || 'Installed binary failed to execute self-check.')
  }

  const actualVersion = decodeOutput(result.stdout)

  if (!isExpectedVersionOutput(actualVersion, expectedVersion)) {
    throw new Error(`Installed binary reported unexpected version: ${actualVersion || '<empty>'}`)
  }
}

export async function upgradeCli(): Promise<void> {
  printTitle()

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

    const release = await response.json() as {
      tag_name: string
      assets?: Array<{ name?: string, digest?: string }>
    }
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
    let expectedHash = release.assets
      ?.find(asset => asset.name === artifactName)
      ?.digest
      ?.replace(/^sha256:/, '')

    if (!expectedHash) {
      const checksumsUrl = `https://github.com/${REPO}/releases/download/v${latestVersion}/${CHECKSUMS_FILE}`

      const checksumsResponse = await fetch(checksumsUrl)
      if (!checksumsResponse.ok) {
        downloadSpin.stop('Download failed.')
        log.error(`Failed to download checksums: HTTP ${checksumsResponse.status}`)
        outro('Update aborted.')
        return
      }

      const checksums = parseChecksumsFile(await checksumsResponse.text())
      expectedHash = checksums.get(artifactName)
    }

    if (!expectedHash) {
      downloadSpin.stop('Download failed.')
      log.error(`Missing checksum for ${artifactName}.`)
      outro('Update aborted.')
      return
    }

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

    const actualHash = await sha256Hex(arrayBuffer)
    if (actualHash !== expectedHash) {
      downloadSpin.stop('Download failed.')
      log.error('Checksum verification failed.')
      outro('Update aborted.')
      return
    }

    const currentExePath = process.execPath
    const transactionId = crypto.randomUUID()
    const targetDir = path.dirname(currentExePath)
    const targetName = path.basename(currentExePath)
    const stagedPath = path.join(targetDir, `${targetName}.new.${transactionId}`)
    const backupPath = path.join(targetDir, `${targetName}.backup.${transactionId}`)
    const updaterPath = path.join(os.tmpdir(), `ycy-updater-${transactionId}${path.extname(currentExePath)}`)
    const statePath = getUpdateStatePath(currentExePath)

    if (readUpdateState(statePath)?.status === 'pending') {
      throw new Error('An update is already in progress.')
    }

    await Bun.write(stagedPath, arrayBuffer)

    if (process.platform !== 'win32') {
      fs.chmodSync(stagedPath, 0o755)
    }
    clearQuarantine(stagedPath)
    verifyBinaryExecutable(stagedPath, latestVersion)

    downloadSpin.stop('Download complete!')

    try {
      fs.copyFileSync(currentExePath, updaterPath, fs.constants.COPYFILE_EXCL)
      if (process.platform !== 'win32') {
        fs.chmodSync(updaterPath, 0o755)
      }
      clearQuarantine(updaterPath)

      const transaction = createUpdateTransaction({
        transactionId,
        parentPid: process.pid,
        targetPath: currentExePath,
        stagedPath,
        backupPath,
        expectedHash,
        expectedVersion: latestVersion,
        statePath,
        updaterPath,
      })
      writeUpdateState(transaction)

      const updater = Bun.spawn([updaterPath, ...getInternalUpdateArgs(transaction)], {
        stdin: 'ignore',
        stdout: 'ignore',
        stderr: 'ignore',
        windowsHide: true,
      })
      updater.unref()
    }
    catch (error) {
      if (fs.existsSync(stagedPath)) {
        fs.unlinkSync(stagedPath)
      }
      if (fs.existsSync(updaterPath)) {
        fs.unlinkSync(updaterPath)
      }
      if (fs.existsSync(statePath)) {
        fs.unlinkSync(statePath)
      }
      throw error
    }

    outro(`Update to ${ansis.green(`v${latestVersion}`)} has been scheduled and will finish after ycy exits.`)
  }
  catch (error) {
    log.error(`Update failed: ${error instanceof Error ? error.message : String(error)}`)
    outro('Update aborted.')
    process.exit(1)
  }
}
