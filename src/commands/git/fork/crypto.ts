import { Buffer } from 'node:buffer'
import { createCipheriv, createDecipheriv, pbkdf2Sync, randomBytes } from 'node:crypto'
import { homedir, platform, userInfo } from 'node:os'
import path from 'node:path'

const ALGORITHM = 'aes-256-gcm'
const PBKDF2_ITERATIONS = 100_000
const KEY_LENGTH = 32
const IV_LENGTH = 16
const SALT_LENGTH = 32

async function getMachineId(): Promise<string> {
  const os = platform()

  if (os === 'darwin') {
    const proc = Bun.spawn(['ioreg', '-rd1', '-c', 'IOPlatformExpertDevice'], {
      stdout: 'pipe',
      stderr: 'pipe',
    })
    const output = await new Response(proc.stdout).text()
    const match = output.match(/"IOPlatformUUID"\s*=\s*"([^"]+)"/)
    if (match?.[1])
      return match[1]
  }
  else if (os === 'linux') {
    const file = Bun.file('/etc/machine-id')
    if (await file.exists()) {
      const id = (await file.text()).trim()
      if (id)
        return id
    }
  }
  else if (os === 'win32') {
    const proc = Bun.spawn(
      ['reg', 'query', 'HKLM\\SOFTWARE\\Microsoft\\Cryptography', '/v', 'MachineGuid'],
      { stdout: 'pipe', stderr: 'pipe' },
    )
    const output = await new Response(proc.stdout).text()
    const match = output.match(/MachineGuid\s+REG_SZ\s+(\S+)/)
    if (match?.[1])
      return match[1]
  }

  // Fallback: hostname + username (less stable but better than nothing)
  const { hostname } = await import('node:os')
  return `${hostname()}-${userInfo().username}`
}

export function generateSalt(): string {
  return randomBytes(SALT_LENGTH).toString('base64')
}

export async function deriveKey(saltBase64: string): Promise<Buffer> {
  const machineId = await getMachineId()
  const username = userInfo().username
  const passphrase = `${machineId}:${username}`
  const salt = Buffer.from(saltBase64, 'base64')
  return pbkdf2Sync(passphrase, salt, PBKDF2_ITERATIONS, KEY_LENGTH, 'sha256')
}

export function encrypt(plaintext: string, key: Buffer): string {
  const iv = randomBytes(IV_LENGTH)
  const cipher = createCipheriv(ALGORITHM, key, iv)
  const encrypted = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()])
  const authTag = cipher.getAuthTag()
  return `${iv.toString('base64')}:${authTag.toString('base64')}:${encrypted.toString('base64')}`
}

export function decrypt(encryptedStr: string, key: Buffer): string {
  const parts = encryptedStr.split(':')
  const iv = Buffer.from(parts[0]!, 'base64')
  const authTag = Buffer.from(parts[1]!, 'base64')
  const ciphertext = Buffer.from(parts[2]!, 'base64')
  const decipher = createDecipheriv(ALGORITHM, key, iv)
  decipher.setAuthTag(authTag)
  return Buffer.concat([decipher.update(ciphertext), decipher.final()]).toString('utf8')
}

export function getConfigDir(): string {
  return path.join(homedir(), '.ycy-cli')
}
