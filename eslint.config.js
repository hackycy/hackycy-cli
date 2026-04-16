// @ts-check
import antfu from '@antfu/eslint-config'

export default antfu(
  {
    type: 'lib',
    ignores: ['CLAUDE.md'],
    rules: {
      'no-console': 'off',
      'ts/explicit-function-return-type': 'off',
    },
  },
)
