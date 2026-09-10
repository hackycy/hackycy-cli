// @ts-check
import antfu from '@antfu/eslint-config'

export default antfu(
  {
    type: 'lib',
    ignores: ['CLAUDE.md', 'mock'],
    rules: {
      'no-console': 'off',
      // Bun adds bun:* to node:module builtinModules, so classify it explicitly
      // to keep editor and Git hook import ordering independent of ESLint's runtime.
      'perfectionist/sort-imports': ['error', {
        customGroups: [
          {
            groupName: 'bun-runtime',
            elementNamePattern: '^bun(?::|$)',
            modifiers: ['value'],
          },
        ],
        groups: [
          'type',
          ['parent-type', 'sibling-type', 'index-type', 'internal-type'],
          'builtin',
          ['external', 'bun-runtime'],
          'internal',
          ['parent', 'sibling', 'index'],
          'side-effect',
          'object',
          'unknown',
        ],
        newlinesBetween: 'ignore',
        order: 'asc',
        type: 'natural',
      }],
      'ts/explicit-function-return-type': 'off',
    },
  },
)
