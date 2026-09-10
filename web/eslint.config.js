// @ts-check
import antfu from '@antfu/eslint-config'

export default antfu(
  {
    type: 'lib',
    ignores: ['dist', 'node_modules'],
    rules: {
      'no-console': 'off',
      'ts/explicit-function-return-type': 'off',
    },
  },
  {
    files: ['pnpm-workspace.yaml'],
    rules: {
      'pnpm/yaml-enforce-settings': 'off',
    },
  },
  {
    files: ['verify-assets.mjs'],
    rules: {
      'antfu/no-top-level-await': 'off',
    },
  },
)
