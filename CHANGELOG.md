# Changelog

## 0.4.1

The menu-bar icon holds still while a model loads, and the speed card
keeps a request between two answers of an agent.

### Fixed

- At the end of each pulse while a model loaded, the engine's logo in
  the menu bar grew a little and moved. It now keeps its size and place.
- The live line of the speed card (the speed now, its change to the
  average and the token count) no longer drops back to the average for a
  moment between two requests: it keeps the request that ended for 15
  seconds, as the menu-bar number does, or until the next one starts.

### Updating

Lumilo 0.2.0 and later download this version by themselves within a day
and offer to restart; the Check button in Settings finds it at once.
Version 0.1.0 cannot update itself: run
`brew upgrade --cask --greedy kotuke/tap/lumilo`.

## 0.4.0

A new look: the menu bar shows the engine you run, and Settings reads
like System Settings.

### New

- **The engine's logo in the menu bar.** The icon is the running engine's
  logo, in one colour like the system's own icons, so macOS tints it for
  the menu bar, a selected item and Increase Contrast. It fades when the
  server stops, pulses while the model loads and breathes while it
  answers; a badge at its corner shows a pause when the model sleeps,
  "!" when something fails and an arrow when the engine is not
  installed. The speed or memory beside it is set like the menu-bar
  clock.
- **The icon alone.** Settings → Menu bar → Show speed and memory turns
  the number beside the icon off.

### Improved

- **Settings in groups.** Engine, the model, Server, Menu bar, General
  and Updates, each under its title, with rows of one height. Updates
  gathers Lumilo, the engine and model updates; each row shows its
  version, or what the check found, under the name. The chosen engine
  has a check on its logo. The host names only a tunnel needs fold away
  under "Tunnel or forwarded port", and the notes are shorter.
- **macOS 26 controls.** Capsule buttons, segments and chips, pop-up
  buttons without frames, round header buttons, switches in the system
  accent; in the dark theme the controls are lighter than the cards.
- The model-updates chip left the main screen: the switch is in
  Settings → Updates.

### Updating

Lumilo 0.2.0 and later download this version by themselves within a day
and offer to restart; the Check button in Settings finds it at once.
Version 0.1.0 cannot update itself: run
`brew upgrade --cask --greedy kotuke/tap/lumilo`.

## 0.3.3

Models and engines changed outside Lumilo show up without a restart.

### Improved

- **The lists keep up with the disk.** Opening the panel looks for the
  engines and models again, and while it is open the model list is
  re-read every 3 seconds. A model pulled with the engine's own command
  or an engine removed with Homebrew shows so at once, not after Lumilo
  restarts.
- **A refresh button** beside "Add model…" in the model list looks again
  at once.

### Updating

Lumilo 0.2.0 and later download this version by themselves within a day
and offer to restart; Settings → Check for updates finds it at once.
Version 0.1.0 cannot update itself: run
`brew upgrade --cask --greedy kotuke/tap/lumilo`.

## 0.3.2

A Splash model whose drafter is gone gets it back in one click.

### Fixed

- When Splash cannot start a model offline because its DFlash2 drafter is
  missing from the Hugging Face cache, the error card names the drafter
  instead of saying the model files are not found, and **Download draft
  model** fetches it. The drafter comes at the version the model was
  installed with, and the model itself is not updated, so “Check for model
  updates” can stay off.

### Updating

Lumilo 0.2.0 and later download this version by themselves within a day
and offer to restart; Settings → Check for updates finds it at once.
Version 0.1.0 cannot update itself: run
`brew upgrade --cask --greedy kotuke/tap/lumilo`.

## 0.3.1

Close speeds stand apart on the chart, and a Splash error names what is
missing.

### Improved

- The speed chart starts below the slowest request, not at zero, so
  requests of close speeds differ at a glance. Its range glides when a new
  request is the slowest or the fastest.

### Fixed

- When Splash cannot start a model offline, the error card quotes the
  error that names the missing files, such as a speculative-decoding
  drafter gone from the Hugging Face cache, not the notice that Splash
  installs the model from that cache.

### Updating

Lumilo 0.2.0 and 0.3.0 download this version by themselves within a day
and offer to restart; Settings → Check for updates finds it at once.
Version 0.1.0 cannot update itself: run
`brew upgrade --cask --greedy kotuke/tap/lumilo`.

## 0.3.0

The speed card shows the request in progress as it runs. This is the
first version that reaches you as an automatic update.

### Improved

- **A live speed card.** While a request runs, the card leads with its
  speed against the average ("47 tok/s now ↑ 7%") and counts its tokens.
  The number, its bar and the count glide between the server's readings,
  as the menu bar does, instead of jumping once a second.
- **An average line on the chart.** A dashed line at the average speed
  shows which requests were faster or slower than usual.
- The bar of the request in progress stands at its real speed instead of
  growing in a loop.
- "Almost full" under memory starts with a capital letter and stands on
  the line of the size beside it.

### Updating from 0.2.0

Lumilo 0.2.0 downloads this version by itself within a day and offers to
restart; Settings → Check for updates finds it at once. Version 0.1.0
cannot update itself: run `brew upgrade --cask --greedy kotuke/tap/lumilo`.

### Updating from 0.1.0

Version 0.1.0 cannot update itself, and Homebrew skips apps that update
themselves in a plain `brew upgrade`. Update it once with
`brew upgrade --cask --greedy kotuke/tap/lumilo`, or download the disk
image below.

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
