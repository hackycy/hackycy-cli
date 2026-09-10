import { randomUUID } from 'node:crypto'
import { mkdir, rename, rm, writeFile } from 'node:fs/promises'
import path from 'node:path'

export async function atomicWrite(filePath: string, contents: string | Uint8Array): Promise<void> {
  await mkdir(path.dirname(filePath), { recursive: true })
  const candidate = `${filePath}.candidate-${randomUUID()}`
  try {
    await writeFile(candidate, contents, { mode: 0o600 })
    await rename(candidate, filePath)
  }
  finally {
    await rm(candidate, { force: true })
  }
}
