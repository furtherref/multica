import baseConfig from "./base.js";
import reactPlugin from "eslint-plugin-react";
import reactHooksPlugin from "eslint-plugin-react-hooks";

/** @type {import("eslint").Linter.Config[]} */
export default [
  ...baseConfig,
  {
    files: ["**/*.{js,jsx,ts,tsx}"],
    plugins: {
      "react-hooks": reactHooksPlugin,
    },
    rules: {
      ...reactHooksPlugin.configs["recommended-latest"].rules,
    },
  },
  {
    files: ["**/*.{jsx,tsx}"],
    plugins: {
      react: reactPlugin,
    },
    rules: {
      ...reactPlugin.configs.recommended.rules,
      ...reactPlugin.configs["jsx-runtime"].rules,
      "react/prop-types": "off",
      "react/no-unknown-property": "off",
    },
    settings: {
      react: { version: "detect" },
    },
  },
];
