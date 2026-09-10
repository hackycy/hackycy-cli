import type { ResolvedCmProfile } from './types'

interface ChatMessage {
  role: 'system' | 'user'
  content: string
}

interface ChatCompletionResponse {
  choices?: Array<{
    finish_reason?: string
    message?: {
      content?: string
    }
  }>
  usage?: {
    prompt_tokens?: number
    completion_tokens?: number
    total_tokens?: number
  }
}

export interface ChatCompletionTokenUsage {
  promptTokens?: number
  completionTokens?: number
  totalTokens?: number
}

export interface ChatCompletionResult {
  content: string
  usage?: ChatCompletionTokenUsage
}

function summarizeText(text: string, limit = 500): string {
  const trimmed = text.trim()
  if (trimmed.length <= limit)
    return trimmed
  return `${trimmed.slice(0, limit)}…`
}

function shouldDisableThinking(profile: ResolvedCmProfile): boolean {
  return profile.baseURL.includes('api.deepseek.com')
    && profile.model.toLowerCase().startsWith('deepseek-v4-')
}

function normalizeTokenUsage(usage: ChatCompletionResponse['usage']): ChatCompletionTokenUsage | undefined {
  if (!usage)
    return undefined

  const promptTokens = Number.isFinite(usage.prompt_tokens) ? usage.prompt_tokens : undefined
  const completionTokens = Number.isFinite(usage.completion_tokens) ? usage.completion_tokens : undefined
  const totalTokens = Number.isFinite(usage.total_tokens)
    ? usage.total_tokens
    : promptTokens !== undefined && completionTokens !== undefined
      ? promptTokens + completionTokens
      : undefined

  if (promptTokens === undefined && completionTokens === undefined && totalTokens === undefined)
    return undefined

  return {
    promptTokens,
    completionTokens,
    totalTokens,
  }
}

export async function createChatCompletionWithUsage(
  profile: ResolvedCmProfile,
  messages: ChatMessage[],
  options: { maxOutputTokens?: number } = {},
): Promise<ChatCompletionResult> {
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), profile.timeoutMs)

  try {
    const res = await fetch(`${profile.baseURL}/chat/completions`, {
      method: 'POST',
      signal: controller.signal,
      headers: {
        'Authorization': `Bearer ${profile.apiKey}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        model: profile.model,
        temperature: profile.temperature,
        max_tokens: options.maxOutputTokens ?? profile.maxOutputTokens,
        messages,
        ...(shouldDisableThinking(profile)
          ? { thinking: { type: 'disabled' } }
          : {}),
      }),
    })

    if (!res.ok) {
      const text = await res.text()
      throw new Error(`${res.status} ${res.statusText}${text ? `: ${text}` : ''}`)
    }

    const raw = await res.text()
    const json = raw ? JSON.parse(raw) as ChatCompletionResponse : {}
    const choice = json.choices?.[0]
    const content = choice?.message?.content?.trim()
    if (!content) {
      const detail = [
        `finish_reason=${choice?.finish_reason ?? 'unknown'}`,
        raw ? `response=${summarizeText(raw)}` : 'response=<empty body>',
      ].join(', ')
      throw new Error(`Provider returned an empty response (${detail})`)
    }

    return {
      content,
      usage: normalizeTokenUsage(json.usage),
    }
  }
  catch (err) {
    if ((err as Error).name === 'AbortError')
      throw new Error(`Provider request timed out after ${profile.timeoutMs}ms`)
    throw err
  }
  finally {
    clearTimeout(timeout)
  }
}

export async function createChatCompletion(
  profile: ResolvedCmProfile,
  messages: ChatMessage[],
): Promise<string> {
  const result = await createChatCompletionWithUsage(profile, messages)
  return result.content
}

export async function testCmProfile(profile: ResolvedCmProfile): Promise<string> {
  const content = await createChatCompletion(profile, [
    {
      role: 'system',
      content: 'Return exactly: ok',
    },
    {
      role: 'user',
      content: 'Connection test.',
    },
  ])
  return content
}
