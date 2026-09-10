import type { ChatCompletionTokenUsage } from '../../../config/client'
import type { ResolvedCmProfile } from '../../../config/types'
import { createChatCompletionWithUsage } from '../../../config/client'

const COMMIT_MESSAGE_MAX_OUTPUT_TOKENS = 4_096

export interface CommitMessageModelInput {
  system: string
  evidence: string
}

export interface CommitMessageModelOutput {
  content: string
  usage?: ChatCompletionTokenUsage
}

export interface CommitMessageModel {
  generate: (input: CommitMessageModelInput) => Promise<CommitMessageModelOutput>
}

export function createOpenAICompatibleCommitMessageModel(profile: ResolvedCmProfile): CommitMessageModel {
  return {
    async generate(input) {
      return createChatCompletionWithUsage(profile, [
        { role: 'system', content: input.system },
        { role: 'user', content: input.evidence },
      ], { maxOutputTokens: COMMIT_MESSAGE_MAX_OUTPUT_TOKENS })
    },
  }
}

export class ScriptedCommitMessageModel implements CommitMessageModel {
  readonly inputs: CommitMessageModelInput[] = []

  constructor(private readonly responses: Array<CommitMessageModelOutput | Error>) {}

  async generate(input: CommitMessageModelInput): Promise<CommitMessageModelOutput> {
    this.inputs.push(input)
    const response = this.responses.shift()
    if (!response)
      throw new Error('No scripted model response remaining.')
    if (response instanceof Error)
      throw response
    return response
  }
}
