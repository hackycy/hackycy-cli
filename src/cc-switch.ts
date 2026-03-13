import fs from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'
import { cancel, isCancel, log, select, spinner, text } from '@clack/prompts'
import { printTitle } from './utils'

export interface CcSwitchOptions {
  env?: string
}

const ENV_FILE = '.env'
const CLAUDA_ENV_KEYS = [
  'ANTHROPIC_API_KEY',
  'ANTHROPIC_BASE_URL',
  'CLAUDE_API_KEY',
  'CLAUDE_BASE_URL',
] as const

type ClaudeEnvKey = typeof CLAUDA_ENV_KEYS[number]

export interface ClaudeEnvConfig {
  [key: string]: string
}

/**
 * 读取 .env 文件
 */
async function readEnvFile(): Promise<ClaudeEnvConfig> {
  try {
    const envPath = path.resolve(ENV_FILE)
    const content = await fs.readFile(envPath, 'utf-8')

    const config: ClaudeEnvConfig = {}
    const lines = content.split('\n')

    for (const line of lines) {
      const trimmed = line.trim()
      // 跳过空行和注释
      if (!trimmed || trimmed.startsWith('#'))
        continue

      // 解析 KEY=VALUE
      const separatorIndex = trimmed.indexOf('=')
      if (separatorIndex === -1)
        continue

      const key = trimmed.slice(0, separatorIndex).trim()
      const value = trimmed.slice(separatorIndex + 1).trim()

      if (CLAUDA_ENV_KEYS.includes(key as ClaudeEnvKey))
        config[key] = value
    }

    return config
  }
  catch {
    // 文件不存在或读取错误，返回空配置
    return {}
  }
}

/**
 * 写入 .env 文件
 */
async function writeEnvFile(config: ClaudeEnvConfig): Promise<void> {
  let content = ''

  try {
    // 读取现有内容
    const envPath = path.resolve(ENV_FILE)
    const existingContent = await fs.readFile(envPath, 'utf-8')
    content = existingContent
  }
  catch {
    // 文件不存在，创建新文件
  }

  const lines = content.split('\n')
  const updatedLines: string[] = []
  const processedKeys = new Set<string>()

  // 处理现有行
  for (const line of lines) {
    const trimmed = line.trim()

    // 跳过空行和注释，但保留它们
    if (!trimmed || trimmed.startsWith('#')) {
      updatedLines.push(line)
      continue
    }

    // 解析 KEY=VALUE
    const separatorIndex = trimmed.indexOf('=')
    if (separatorIndex === -1) {
      updatedLines.push(line)
      continue
    }

    const key = trimmed.slice(0, separatorIndex).trim()

    if (CLAUDA_ENV_KEYS.includes(key as ClaudeEnvKey)) {
      // 如果是 Claude 环境变量，更新或删除
      const value = config[key]
      if (value) {
        updatedLines.push(`${key}=${value}`)
      }
      processedKeys.add(key)
    }
    else {
      // 保留其他变量
      updatedLines.push(line)
    }
  }

  // 添加新的配置
  for (const [key, value] of Object.entries(config)) {
    if (!processedKeys.has(key) && value) {
      updatedLines.push(`${key}=${value}`)
    }
  }

  // 写入文件
  const envPath = path.resolve(ENV_FILE)
  await fs.writeFile(envPath, `${updatedLines.join('\n')}\n`, 'utf-8')
}

/**
 * 查看 Claude 环境变量配置
 */
export async function ccSwitchView(): Promise<void> {
  printTitle()

  const spin = spinner()
  spin.start('Reading Claude environment configuration...')

  const config = await readEnvFile()
  spin.stop('Configuration loaded.')

  if (Object.keys(config).length === 0) {
    log.warn('No Claude environment variables found in .env file.')
    log.message('Use `ycy cc-switch set` to configure Claude environment variables.')
    return
  }

  log.success('Current Claude environment configuration:')
  console.log()

  const tableData = Object.entries(config).map(([key, value]) => ({
    Key: key,
    Value: maskValue(value),
  }))

  console.table(tableData)
  console.log()

  // 同时显示系统环境变量
  log.message('System environment variables:')
  console.log()

  const systemData: { Key: string, Value: string }[] = []
  for (const key of CLAUDA_ENV_KEYS) {
    const value = process.env[key]
    if (value) {
      systemData.push({
        Key: key,
        Value: maskValue(value),
      })
    }
  }

  if (systemData.length > 0) {
    console.table(systemData)
  }
  else {
    log.message('No Claude environment variables found in system.')
  }
}

/**
 * 遮蔽敏感信息
 */
function maskValue(value: string): string {
  if (value.length <= 8)
    return '***'

  return `${value.slice(0, 4)}${'*'.repeat(8)}${value.slice(-4)}`
}

/**
 * 设置 Claude 环境变量
 */
export async function ccSwitchSet(options: CcSwitchOptions): Promise<void> {
  printTitle()

  // 读取现有配置
  const existingConfig = await readEnvFile()

  let key: ClaudeEnvKey

  if (options.env) {
    // 使用命令行指定的环境变量名
    if (!CLAUDA_ENV_KEYS.includes(options.env as ClaudeEnvKey)) {
      cancel(`Invalid environment variable: ${options.env}`)
      log.message(`Supported keys: ${CLAUDA_ENV_KEYS.join(', ')}`)
      return
    }
    key = options.env as ClaudeEnvKey
  }
  else {
    // 交互式选择
    const selected = await select<ClaudeEnvKey>({
      message: 'Select Claude environment variable to configure:',
      options: CLAUDA_ENV_KEYS.map(k => ({
        value: k,
        label: `${k}${existingConfig[k] ? ' (currently set)' : ''}`,
      })),
    })

    if (isCancel(selected)) {
      cancel('Operation cancelled.')
      return
    }

    key = selected
  }

  const currentValue = existingConfig[key]
  const input = await text({
    message: `Enter value for ${key}:`,
    placeholder: currentValue || 'e.g., sk-ant-api03-...',
    defaultValue: currentValue,
    validate(value) {
      if (!value || value.trim().length === 0)
        return 'Please enter a value.'
      return undefined
    },
  })

  if (isCancel(input)) {
    cancel('Operation cancelled.')
    return
  }

  const spin = spinner()
  spin.start('Saving configuration...')

  // 更新配置
  const newConfig = { ...existingConfig }
  newConfig[key] = input

  await writeEnvFile(newConfig)

  spin.stop('Configuration saved successfully.')

  log.success(`Environment variable ${key} has been updated.`)
  log.message(`Configuration saved to ${path.resolve(ENV_FILE)}`)
}
