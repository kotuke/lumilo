# Changelog

## Unreleased

- Install with Homebrew: `brew install --cask kotuke/tap/lumilo`.
- The README shows Lumilo starting a server in a demo GIF.
- Splash's model list shows the models already on this Mac, for example
  downloaded for llama.cpp, and adds one from those files in seconds.
- Claude Code, Codex, OpenCode and Pi also launch against llama.cpp and
  TensorFold servers, without changes to the agents' own settings. Claude
  Code's button is off while the context is under the 100K it needs, Pi's
  under 64K.
- The speed in the menu bar glides instead of jumping, and stays while a
  coding agent runs its tools between answers; a request's bar in the chart
  is its whole speed.
- Pause a model download from its row and resume it later from what it
  fetched.
- A third engine: [TensorFold](https://github.com/ashhart/TensorFold), the
  native server for MLX checkpoints. Lumilo installs it with Homebrew,
  downloads its models into the Hugging Face cache, unloads the model when
  idle, and downloads Qwen3.8-27B with its DFlash2 drafter, which runs by
  default and turns off in the model's settings. With the context on Auto,
  Lumilo serves the largest that fits in memory (64K or 128K for Qwen3.8-27B
  on 48 GB) instead of TensorFold's own 32K.

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
