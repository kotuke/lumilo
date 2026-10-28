# Changelog

## Unreleased

- Splash's model list shows the models already on this Mac, for example
  downloaded for llama.cpp, and adds one from those files in seconds.

## 0.1.0

The first release: a menu-bar panel for the LLM engines on your Mac.

- Runs [Splash](https://github.com/incoai/splash) or
  [llama.cpp](https://github.com/ggml-org/llama.cpp): start, stop and
  restart the server, with the reason when a start fails.
- Shows memory against the engine's budget, the generation speed, time to
  first token and the last 20 requests, also in the menu-bar icon.
- Gets models from Hugging Face: search as you type, see whether a variant
  fits in memory, download beside a running server, resume after a quit,
  delete with the space it frees.
- Opens Claude Code, Codex, OpenCode or Pi pointed at the local Splash
  server, in Terminal, Ghostty or Rex.
- Installs and updates the engine with Homebrew.
- Network access only with an API key, kept in the Keychain.
- English, Russian, German, French, Spanish, Japanese, Chinese (Simplified)
  and Arabic.

Requires macOS on Apple silicon (Splash needs macOS 26.4 or later) and
Homebrew. Signed with Developer ID and notarized by Apple.
