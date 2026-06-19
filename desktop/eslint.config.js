const js = require('@eslint/js');
const reactHooks = require('eslint-plugin-react-hooks');
const reactRefreshModule = require('eslint-plugin-react-refresh');
const tseslint = require('typescript-eslint');

const reactRefresh = reactRefreshModule.default ?? reactRefreshModule;

module.exports = tseslint.config(
  {
    ignores: ['out', 'dist', 'node_modules', 'scripts', 'build', '*.js'],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['src/**/*.{ts,tsx}', 'electron.vite.config.ts'],
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-hooks/refs': 'off',
      'react-hooks/set-state-in-effect': 'off',
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
    },
  },
);
