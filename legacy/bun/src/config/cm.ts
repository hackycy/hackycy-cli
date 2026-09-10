import type { CmConfig, CmProfile, ResolvedCmProfile } from './types'
import process from 'node:process'
import { decrypt, deriveKey, encrypt } from './crypto'
import { readConfig, updateConfig } from './store'

const DEFAULT_TEMPERATURE = 0.2
const DEFAULT_TIMEOUT_MS = 300_000
const DEFAULT_MAX_OUTPUT_TOKENS = 1000

const ENV_PROFILE = 'YCY_CM_PROFILE'
const ENV_BASE_URL = 'YCY_CM_BASE_URL'
const ENV_API_KEY = 'YCY_CM_API_KEY'
const ENV_MODEL = 'YCY_CM_MODEL'
const ENV_TEMPERATURE = 'YCY_CM_TEMPERATURE'
const ENV_TIMEOUT_MS = 'YCY_CM_TIMEOUT_MS'
const ENV_MAX_OUTPUT_TOKENS = 'YCY_CM_MAX_OUTPUT_TOKENS'

function emptyCmConfig(): CmConfig {
  return { profiles: {} }
}

function normalizeBaseURL(baseURL: string): string {
  return baseURL.trim().replace(/\/+$/, '')
}

function parseNumberEnv(value: string | undefined): number | undefined {
  if (!value)
    return undefined
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : undefined
}

function withDefaults(profile: CmProfile): Omit<ResolvedCmProfile, 'name' | 'apiKey'> {
  return {
    baseURL: normalizeBaseURL(profile.baseURL),
    model: profile.model,
    temperature: profile.temperature ?? DEFAULT_TEMPERATURE,
    timeoutMs: profile.timeoutMs ?? DEFAULT_TIMEOUT_MS,
    maxOutputTokens: profile.maxOutputTokens ?? DEFAULT_MAX_OUTPUT_TOKENS,
  }
}

export async function readCmConfig(): Promise<CmConfig> {
  const config = await readConfig()
  if (!config.cm)
    return emptyCmConfig()
  return {
    ...config.cm,
    profiles: config.cm.profiles ?? {},
  }
}

export async function writeCmConfig(cm: CmConfig): Promise<void> {
  await updateConfig((config) => {
    config.cm = cm
  })
}

export async function addCmProfile(
  name: string,
  baseURL: string,
  model: string,
  apiKey: string,
): Promise<void> {
  await updateConfig(async (config) => {
    const cm = config.cm ?? emptyCmConfig()
    const key = await deriveKey(config.salt)
    cm.profiles[name] = {
      baseURL: normalizeBaseURL(baseURL),
      model: model.trim(),
      apiKey: encrypt(apiKey, key),
    }
    cm.defaultProfile ??= name
    config.cm = cm
  })
}

export async function removeCmProfile(name: string): Promise<boolean> {
  return updateConfig((config) => {
    const cm = config.cm ?? emptyCmConfig()
    if (!cm.profiles[name])
      return false
    delete cm.profiles[name]
    if (cm.defaultProfile === name)
      cm.defaultProfile = Object.keys(cm.profiles)[0]
    config.cm = cm
    return true
  })
}

export async function setDefaultCmProfile(name: string): Promise<void> {
  await updateConfig((config) => {
    const cm = config.cm ?? emptyCmConfig()
    if (!cm.profiles[name])
      throw new Error(`CM profile not found: ${name}`)
    cm.defaultProfile = name
    config.cm = cm
  })
}

export async function setCmProfileValue(name: string, key: string, value: string): Promise<void> {
  await updateConfig(async (config) => {
    const cm = config.cm ?? emptyCmConfig()
    const profile = cm.profiles[name]
    if (!profile)
      throw new Error(`CM profile not found: ${name}`)

    if (key === 'baseURL') {
      profile.baseURL = normalizeBaseURL(value)
    }
    else if (key === 'model') {
      profile.model = value.trim()
    }
    else if (key === 'apiKey') {
      const cryptoKey = await deriveKey(config.salt)
      profile.apiKey = encrypt(value, cryptoKey)
    }
    else if (key === 'temperature') {
      const parsed = Number(value)
      if (!Number.isFinite(parsed) || parsed < 0 || parsed > 2)
        throw new Error('temperature must be a number between 0 and 2')
      profile.temperature = parsed
    }
    else if (key === 'timeoutMs') {
      const parsed = Number.parseInt(value, 10)
      if (!Number.isInteger(parsed) || parsed < 1000)
        throw new Error('timeoutMs must be an integer greater than or equal to 1000')
      profile.timeoutMs = parsed
    }
    else if (key === 'maxOutputTokens') {
      const parsed = Number.parseInt(value, 10)
      if (!Number.isInteger(parsed) || parsed < 32)
        throw new Error('maxOutputTokens must be an integer greater than or equal to 32')
      profile.maxOutputTokens = parsed
    }
    else {
      throw new Error('Unsupported key. Use baseURL, model, apiKey, temperature, timeoutMs, or maxOutputTokens.')
    }

    config.cm = cm
  })
}

export async function listCmProfiles(): Promise<CmConfig> {
  return readCmConfig()
}

export async function resolveCmProfile(profileName?: string, timeoutOverrideMs?: number): Promise<ResolvedCmProfile> {
  const config = await readConfig()
  const cm = config.cm ?? emptyCmConfig()
  const env = process.env
  const selectedName = profileName || env[ENV_PROFILE] || cm.defaultProfile || Object.keys(cm.profiles)[0]
  const stored = selectedName ? cm.profiles[selectedName] : undefined

  const baseURL = env[ENV_BASE_URL] || stored?.baseURL
  const model = env[ENV_MODEL] || stored?.model
  let apiKey = env[ENV_API_KEY]

  if (!apiKey && stored?.apiKey) {
    const key = await deriveKey(config.salt)
    apiKey = decrypt(stored.apiKey, key)
  }

  if (!baseURL || !model || !apiKey) {
    throw new Error('No usable CM profile found. Run "ycy config cm add" or set YCY_CM_BASE_URL, YCY_CM_MODEL, and YCY_CM_API_KEY.')
  }

  const defaults = stored
    ? withDefaults(stored)
    : {
        baseURL: normalizeBaseURL(baseURL),
        model,
        temperature: DEFAULT_TEMPERATURE,
        timeoutMs: DEFAULT_TIMEOUT_MS,
        maxOutputTokens: DEFAULT_MAX_OUTPUT_TOKENS,
      }

  return {
    name: selectedName || 'env',
    baseURL: normalizeBaseURL(baseURL),
    model,
    apiKey,
    temperature: parseNumberEnv(env[ENV_TEMPERATURE]) ?? defaults.temperature,
    timeoutMs: timeoutOverrideMs ?? parseNumberEnv(env[ENV_TIMEOUT_MS]) ?? defaults.timeoutMs,
    maxOutputTokens: parseNumberEnv(env[ENV_MAX_OUTPUT_TOKENS]) ?? defaults.maxOutputTokens,
  }
}
