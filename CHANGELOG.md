# Changelog

## 0.2.0

Lumilo now keeps itself up to date, runs a third engine, TensorFold, and
opens coding agents against every engine.

### New

- **Lumilo updates itself.** It checks its releases once a day, downloads a
  new version in the background and checks its signature. A dot on the
  Settings button says the version is ready. The restart asks first while
  the server runs or a model downloads, and the new version starts them
  again. Settings shows Lumilo's version, checks for updates and turns the
  automatic download off.
- **TensorFold**, the native server for MLX checkpoints, is the third
  engine. Lumilo installs it with Homebrew, downloads its models into the
  Hugging Face cache and unloads the model when idle. Qwen3.8-27B comes with
  its DFlash2 drafter, on by default and off in the model's settings. With
  the context on Auto, Lumilo serves the largest that fits in memory (64K or
  128K for Qwen3.8-27B on 48 GB) instead of TensorFold's own 32K.
- **Coding agents for every engine.** Claude Code, Codex, OpenCode and Pi
  also open against llama.cpp and TensorFold, without changes to the agents'
  own settings. Claude Code's button is off while the context is under the
  100K it needs, Pi's under 64K.
- **Pause downloads.** Pause a model download from its row and resume it
  later from what it fetched.
- **Models already on this Mac.** Splash's model list shows models found in
  the Hugging Face cache, for example downloaded for llama.cpp, and adds one
  from those files in seconds.
- **Install with Homebrew:** `brew install --cask kotuke/tap/lumilo`.

### Improved

- The server card shows the context the server serves beside the address
  ("↔ 64K"). A click opens Settings, where the context row blinks twice.
- The speed in the menu bar glides instead of jumping and stays while an
  agent runs its tools between answers. A request's bar in the chart is its
  whole speed.
- Settings' lists are as wide as their longest item, so a drafter's name
  shows whole.
- The engine menu keeps 2 pt between rows, so a highlighted row never
  touches the next one.

### Updating from 0.1.0

Version 0.1.0 cannot update itself. Update it once with
`brew upgrade --cask kotuke/tap/lumilo`, or download the disk image below.
From 0.2.0 on, Lumilo updates itself.

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
