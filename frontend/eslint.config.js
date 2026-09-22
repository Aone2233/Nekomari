import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  { ignores: ['dist'] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      // react-hooks v7 folds the React Compiler rules into `recommended`: 14
      // extra rules that flag 133 pre-existing patterns in this app (ref writes
      // during render, setState inside an effect, manual memoization). Adopting
      // those is its own change with its own review, so the two rules this
      // project has always enforced are named explicitly instead of spreading
      // `recommended`. Nothing is silenced -- the compiler rules are simply not
      // enabled yet, and turning them on is a deliberate follow-up.
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
      'react-refresh/only-export-components': [
        'warn',
        { allowConstantExport: true },
      ],
      '@typescript-eslint/no-explicit-any': 'off',
      'prefer-const': 'off',
    },
  },
)
