import eslint from "@eslint/js";
import { defineConfig, globalIgnores } from "eslint/config";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";
import tseslint from "typescript-eslint";

const typedFiles = ["**/*.{ts,tsx}"];

function forbidHigherLayers(patterns) {
  return [
    "error",
    {
      patterns: patterns.map((layer) => ({
        group: [`../${layer}`, `../${layer}/*`, `../${layer}/**`, `@/features/admin/${layer}/*`, `@/features/admin/${layer}/**`],
        message: `Keep ${layer} dependencies in the higher-level layer.`,
      })),
    },
  ];
}

export default defineConfig([
  globalIgnores([
    ".next/**",
    ".next-e2e/**",
    "node_modules/**",
    "next-env.d.ts",
    "playwright-report/**",
    "test-results/**",
  ]),
  {
    files: ["**/*.{js,mjs,cjs}"],
    ...eslint.configs.recommended,
    languageOptions: {
      ...eslint.configs.recommended.languageOptions,
      globals: globals.node,
    },
  },
  {
    files: typedFiles,
    extends: [tseslint.configs.recommended],
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
    },
    linterOptions: {
      reportUnusedDisableDirectives: "error",
    },
    plugins: {
      "react-hooks": reactHooks,
    },
    rules: {
      "@typescript-eslint/no-explicit-any": "off",
      "@typescript-eslint/no-unused-vars": [
        "error",
        {
          argsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
        },
      ],
      "react-hooks/exhaustive-deps": "error",
      "react-hooks/rules-of-hooks": "error",
    },
  },
  {
    files: ["features/admin/{core,domain,i18n,shared}/**/*.{ts,tsx}"],
    rules: {
      "no-restricted-imports": forbidHigherLayers(["resources", "views", "shell"]),
    },
  },
  {
    files: ["features/admin/resources/**/*.{ts,tsx}"],
    rules: {
      "no-restricted-imports": forbidHigherLayers(["views", "shell"]),
    },
  },
  {
    files: ["features/admin/views/**/*.{ts,tsx}"],
    rules: {
      "no-restricted-imports": forbidHigherLayers(["shell"]),
    },
  },
]);
