import js from "@eslint/js";
import globals from "globals";

// The rule that earns its keep here is no-undef.
//
// This UI was one 4,400-line IIFE, where every function saw every other by
// virtue of sharing a scope. Splitting it into modules means a call to a
// function you forgot to import is no longer a scope lookup — it is a
// reference to an undefined global, which bundles without complaint and throws
// the first time that code path runs. no-undef is what turns that into a
// build-time error instead of a broken tab nobody clicked yet.
export default [
  js.configs.recommended,
  {
    files: ["src/**/*.js"],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: "module",
      globals: globals.browser,
    },
    rules: {
      "no-undef": "error",
      "no-unused-vars": ["error", { args: "none" }],
    },
  },
];
