import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      // Ban dangerouslySetInnerHTML in the operator UI: it is the XSS→RCE surface a security
      // gateway must not carry (the webview reaches external authenticated resources). React
      // escapes by default; if raw HTML ever seems necessary, sanitize in the daemon and render
      // as text instead. See docs/findings/2026-07-06-ui-security-hardening.md and ADR-0041.
      'no-restricted-syntax': [
        'error',
        {
          selector: "JSXAttribute[name.name='dangerouslySetInnerHTML']",
          message:
            'dangerouslySetInnerHTML is forbidden (XSS→RCE surface in the operator webview) — see docs/findings/2026-07-06-ui-security-hardening.md.',
        },
      ],
    },
  },
])
