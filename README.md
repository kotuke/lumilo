<div align="center">

<img src="docs/images/icon.png" alt="Lumilo app icon" width="128" height="128">

# Lumilo

**A menu-bar panel for the LLM engines on your Mac.**<br>
Start the server, watch memory and speed, get models from Hugging Face,
and point coding agents at it, without a terminal.

[![CI](https://github.com/kotuke/lumilo/actions/workflows/ci.yml/badge.svg)](https://github.com/kotuke/lumilo/actions/workflows/ci.yml)
![macOS on Apple silicon](https://img.shields.io/badge/macOS-Apple%20silicon-black?logo=apple)
![Engines: Splash, llama.cpp and TensorFold](https://img.shields.io/badge/engines-Splash%20%C2%B7%20llama.cpp%20%C2%B7%20TensorFold-f26b4f)
![8 languages](https://img.shields.io/badge/languages-8-4b6bfb)
![Signed with Developer ID and notarized](https://img.shields.io/badge/Developer%20ID-signed%20%26%20notarized-1f6feb?logo=apple)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/hero-dark.png">
  <img src="docs/images/hero-light.png" alt="Lumilo panels, one per engine: llama.cpp serving Qwen3.8-27B while Qwen3.6-35B-A3B downloads, TensorFold generating with Qwen3.8-27B and its DFlash2 drafter, Splash looking up a model's variants on Hugging Face">
</picture>

</div>

Lumilo runs [Splash](https://github.com/incoai/splash),
[llama.cpp](https://github.com/ggml-org/llama.cpp) (`llama-server`) or
[TensorFold](https://github.com/ashhart/TensorFold) for you.
It starts and stops the server, shows its state, memory and generation
speed, downloads and deletes models, launches coding agents against it, and
installs or updates the engine. One engine runs at a time; you choose it in
Settings. Engines are pluggable; [mlx-serve](https://github.com/ddalcu/mlx-serve) and
[oMLX](https://github.com/jundot/omlx) are planned.

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/images/demo-dark.gif">
    <img src="docs/images/demo-light.gif" width="379" alt="Lumilo starts Splash with Qwen3.8-27B: the model loads, requests come in, the speed rises in the menu-bar icon and joins the chart">
  </picture>
</p>

```bash
brew install --cask kotuke/tap/lumilo
```

> **Unofficial.** Lumilo is an independent project. It is not affiliated with,
> endorsed by, or supported by the authors of Splash or any other engine it
> controls. Engine names and logos belong to their owners.

## Highlights

- **One click to serve.** Start, stop and restart the server; see whether it
  is loading, ready, generating or unloaded, and why a start failed.
- **Live numbers.** Memory against the engine's budget, tokens per second,
  time to first token, the last 20 requests as a chart, in the menu bar too.
- **Models from Hugging Face.** Search as you type, see each variant's size
  and whether it fits in memory, download beside a running server, delete
  with the space it frees.
- **Coding agents.** Claude Code, Codex, OpenCode or Pi in Terminal, Ghostty
  or Rex, already pointed at the local server of any engine.
- **Private by default.** The server listens on this Mac only; network access
  needs an API key, kept in the Keychain.
- **Native and translated.** Liquid Glass, light and dark themes, eight
  languages with a right-to-left layout for Arabic.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/features-dark.png">
  <img src="docs/images/features-light.png" alt="First-run model suggestions, TensorFold's model list with image input and a DFlash2 drafter, llama.cpp open to the network with an API key, the delete dialog">
</picture>

## In the menu bar

The icon tells the state without opening the panel: the bar fills with the
memory the model takes, and the value is the live speed while it generates,
the memory while it waits, or a sign when it loads, sleeps, stops or fails.
llama.cpp counts a request's tokens when it ends, so under llama.cpp the icon
shows memory while it generates. TensorFold does too, but it reports the
tokens in flight, so Lumilo times them itself and the speed shows live. The
speed is that of the last three seconds, and the number glides to a new one
when the icon is animated; it stays for 15 seconds after an answer, while a
coding agent runs its tools between two of them.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/menubar-dark.png">
  <img src="docs/images/menubar-light.png" alt="The menu-bar icon: generating, ready, loading, unloaded when idle, stopped, failed to start">
</picture>

## Light and dark

The panel follows the system theme, or the one you switch to in its header.

<img src="docs/images/themes.png" alt="The same llama.cpp panel with the list of the three engines open, in the light and the dark theme">

## Languages

English, Russian, German, French, Spanish, Japanese, Chinese (Simplified) and
Arabic. Lumilo follows the system language, or the one you pick in Settings.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/languages-dark.png">
  <img src="docs/images/languages-light.png" alt="The Lumilo panel in eight languages, under Splash, llama.cpp and TensorFold">
</picture>

## Install

With [Homebrew](https://brew.sh):

```bash
brew install --cask kotuke/tap/lumilo
```

Then open Lumilo from Applications. Lumilo updates itself (see
[Updates](#updates)), so Homebrew leaves it alone after the install.
Version 0.1.0 could not update itself yet: update it once with
`brew upgrade --cask kotuke/tap/lumilo` or from the
[latest release](https://github.com/kotuke/lumilo/releases/latest).

Or by hand:

1. Download the `.dmg` from the
   [latest release](https://github.com/kotuke/lumilo/releases/latest).
2. Open it and drag Lumilo to Applications.
3. Open Lumilo: its icon appears in the menu bar. If no engine is installed,
   the panel offers to install it with Homebrew.

Lumilo is signed with an Apple Developer ID certificate and notarized by
Apple, so macOS opens it without a security warning. Check it with:

```bash
spctl --assess --verbose /Applications/Lumilo.app
```

which prints `accepted` and `source=Notarized Developer ID`.

## All features

- **Server control.** Start, stop and restart the server with the selected
  model. Lumilo runs it as a child process in its own process group and
  stops it when you quit. A server started elsewhere (for example in a
  terminal) is detected and shown as running outside the panel.
- **State at a glance.** The menu-bar icon shows memory in use or the live
  generation speed. The panel shows loading, ready, generating, unloaded,
  not responding, stopping and error states, with the reason and the last
  log line when a start fails (port in use, missing model, not enough
  memory). Beside the address, a chip shows the model's context ("↔ 64K"):
  the server's while it runs, else the one chosen in Settings.
- **Metrics.** Memory against the engine's budget, conversation state on
  disk, average speed over the last 10 requests, the last 20 requests as a
  chart with a line at the average, time to first token and prompt speed.
  While a request runs, the card shows its live speed against the average
  ("47 tok/s now ↑ 7%") and counts its tokens.
- **Unload and reload** the model weights without stopping the server, and
  unload automatically when idle. llama.cpp and TensorFold unload only when
  idle.
- **Models.** Pick an installed model, or add one by its Hugging Face name
  (`owner/repo:variant`). It downloads in the model list, apart from the
  server, which keeps serving its model: a progress bar with the speed and
  the time left, Pause and Cancel buttons, the reason and Retry or Remove
  when it fails. A paused download shows "Paused" with what it fetched,
  Resume and Remove. Several downloads queue: one runs per engine, the others wait as
  "Queued". Downloads that did not finish survive quitting Lumilo
  (`downloads.json` beside the settings) and show as "Paused" with what they
  fetched, Resume and Remove; nothing starts on its own at launch. A switch
  to another engine pauses its downloads, and an engine update holds them
  and goes on after. llama.cpp and TensorFold resume a file from its partial
  blob (Range, then a SHA-256 or Git blob check); Splash's installer keeps finished files but fetches
  an unfinished one again. It can be picked once it is installed. Under
  Splash the list also shows the models "Already on this Mac": GGUFs in the
  Hugging Face cache that Splash serves but has not installed, as llama.cpp
  downloads them. Add installs one from those files and fetches only what
  is missing (the vision projector, the DFlash2 draft). The
  trash button of a model deletes it from the disk after a confirmation
  that says how much it frees; it stops the server first when the server
  runs that model, and keeps files another installed model shares (the
  draft of a model family, a vision projector). Under TensorFold,
  Qwen3.8-27B downloads with its DFlash2 drafter, which runs by default and
  turns off with the model's speculative decoding switch. The dialog warns when the
  files are in the Hugging Face cache, which other apps may read too. A GGUF repository without a
  vision projector downloads for text only on Splash, which refuses it with
  image input; a model that fails for that reason starts for text only in
  one click. The field
  completes as you type: from two letters it lists matching Hugging Face
  repositories by downloads (GGUF ones for llama.cpp; GGUF and MLX ones of
  the architectures Splash runs for Splash; MLX ones of the families
  TensorFold serves for TensorFold, added as `owner/repo`), and after a GGUF repository and
  `:` it lists the variants with their sizes, 4-bit first, and warns when one
  does not fit in memory. Arrows move, Enter or Tab take, Esc closes; it says
  when nothing was found, when a repository has no variant the engine runs,
  and when Hugging Face is unreachable (the field still works without the
  list). With no model
  yet, the panel suggests Qwen3.8-27B and Qwen3.6-35B-A3B, with their sizes
  and whether they fit in memory.
- **Model updates** chip in the server card: a toggle for the setting of the
  same name (off starts the server offline); while the server runs, the card
  says the change applies on restart and offers Restart.
- **Agents.** One click opens a terminal (Terminal, Ghostty or Rex; pick it in
  Settings) with Claude Code, Codex, OpenCode or Pi pointed at the local server.
  Splash launches them itself; for llama.cpp and TensorFold Lumilo passes the
  server, the model and the key in the environment and the command line, and
  leaves the agents' own configuration files alone (Pi gets a provider
  extension and, under TensorFold, Codex a home of its own in Lumilo's
  folder, `~/Library/Application Support/Lumilo/agents`). Claude Code needs a
  context of 100K tokens or more and Pi 64K: against a smaller one the button is off and
  its tooltip says so.
- **Install and update** the engine through Homebrew. Lumilo checks for a new
  version once a day and updates only when you click.
- <a id="updates"></a>**Updates of Lumilo.** Lumilo checks its GitHub releases
  once a day, downloads a new version in the background and checks its
  signature; the running app stays as it is. A dot on the Settings button
  says the version is ready, and Settings restarts into it. When the server
  runs or a model downloads, the restart asks first, and the new version
  starts the server again with the same model and resumes the downloads.
  Turn the automatic download off in Settings to install each version with a
  click. A build of your own never downloads by itself: it has no stapled
  notarization ticket, and Lumilo offers its updates instead. A copy that
  cannot replace itself (in a folder you cannot write to, or opened from the
  disk image) says to update it with Homebrew.
- **Settings.** Engine; access from this computer or from the network; port;
  API key (kept in the macOS Keychain; the network access never starts without
  one); allowed host names (Splash); context size, vision and draft per model
  of each engine; idle unload; model updates; Lumilo's version and its
  automatic updates; language; launch at login; icon animation.
- **Look.** Liquid Glass cards; light and dark themes follow the system until
  you pick one with the header button.
- **Languages.** English, Arabic, Chinese (Simplified), French, German,
  Japanese, Russian and Spanish. Arabic uses a right-to-left layout. See
  [Translations](#translations).

## Requirements

- macOS on Apple silicon. Splash itself needs macOS 26.4 or later.
- [Homebrew](https://brew.sh) to install the engine.

## Build

Lumilo is written in Go with [MyGo](https://github.com/egoist/mygo).

```bash
go tool mygo dev        # run a development build that restarts on change
CGO_ENABLED=0 go test ./...
go tool mygo vet .
go tool mygo build      # build/darwin-arm64/Lumilo.app
```

Test the server lifecycle, Homebrew and the login item from the built app in
`/Applications`: `mygo dev` runs the app with your shell's environment and
kills its process group on every rebuild.

The images of this README come from the panel's screenshot tests:
`python3 scripts/readme-images.py` (it needs Pillow and ffmpeg) renders them
again into `docs/images`, the demo GIF from the frames of `TestDemo`.

A release build is signed with Developer ID, its disk image notarized and its
update archive signed: `scripts/setup-release-signing.sh` sets up the
certificate and keys once, then `scripts/release.sh` builds. It packs the
app again into a disk image with an arrow to Applications
(`scripts/make-dmg.sh`, background drawn by `packaging/dmg.py`); Finder lays
its window out, so the first run asks to let the terminal control Finder.
With `-upload` it also puts an install line at the top of the release's
notes (`scripts/release-notes.sh`): the release lists the update archive,
the deltas and the manifest beside the disk image, and they are for
Lumilo's own updates.
After the release, move the cask in
[kotuke/homebrew-tap](https://github.com/kotuke/homebrew-tap) to the new
version: `brew bump-cask-pr --version <version> kotuke/tap/lumilo` opens
the pull request. The cask has `auto_updates true` from 0.3.0 on, so
`brew upgrade` leaves Lumilo to update itself; 0.1.0 has no updater, and
`brew upgrade --greedy` moves it on. Keep a copy of the update key
(`~/.config/lumilo/update-keys/`) off this Mac: installed copies accept only
updates signed with it.

A live test against a real Splash server (it loads a model) runs with:

```bash
CGO_ENABLED=0 go test -tags live -run 'TestLive$' -v -timeout 20m ./internal/core/
```

`TestLiveUpdate` upgrades the installed engine through Homebrew while a
server runs and checks that it restarts on the new version. It changes
your system, so it runs only with `LUMILO_LIVE_UPDATE=1` and only when an
update exists.

## Translations

English and Russian are written by the author. The other languages
(Arabic, Chinese, French, German, Japanese, Spanish) were translated
without a review by native speakers, so some phrases may read awkwardly
or be wrong. If you spot one, please
[open an issue](https://github.com/kotuke/lumilo/issues/new) or send a pull
request: the strings are in `locales/<language>.json`.

## Architecture

| Package | Role |
| --- | --- |
| `internal/engine` | The contract. `Engine`: `Info()` (ID, name, logo, capabilities, suggestions, model search, agents), `Installer()`, models, the serve command (context size, download on start), the optional `Downloader` (download apart from the server), output parsing, failure classes, download meter, probing, release and restore, agent commands. `Installer`: `Describe()` (method, size on disk, commands, prerequisite), `Detect`, `Install`, `Upgrade`, `Latest`, `ReleaseNotesURL`. The memory estimate (`Estimate`, `Judge`) |
| `internal/engines/enginekit` | What engines share: the local HTTP request with a bearer key, the Prometheus text parser, executable lookup at Homebrew paths, the child environment, embedded logos, release feeds (GitHub, Homebrew formula API, a tap's formula), the Hugging Face hub cache and its download meter, and `Homebrew`, the installer of an engine with a Homebrew formula |
| `internal/engines/splash` | The Splash implementation. Everything specific to Splash lives here: formula, lock files, log lines, metric names, error texts |
| `internal/engines/llamacpp` | The llama.cpp (`llama-server`) implementation: GGUF models from the Hugging Face cache, `/health`, `/props` and `/metrics`, log lines, error texts, suggestions |
| `internal/engines/tensorfold` | The TensorFold implementation: MLX checkpoints of the families its native engine serves, from the Hugging Face cache, DFlash2 drafters, `/health`, `/memory` and `/metrics`, idle unload, log lines, error texts |
| `internal/core` | The engine-agnostic supervisor. It installs and updates engines through their `Installer`, runs the server, switches engines, runs model downloads apart from the server (`DownloadModel`, `View.Downloads`), polls the server, keeps the speed history and publishes a `View` |
| `internal/engines/makers` | Logos of model makers (Qwen) for the suggestions |
| `internal/appupdate` | Lumilo's own updates: the daily check of the release feed (`mygo.Updater`), the background download and install, the state the panel shows |
| `internal/panel` | The panel and settings screens, drawn from a `View` |
| `internal/trayicon` | The menu-bar icon |
| `internal/settings`, `internal/terminal` | Settings file, Terminal launcher |
| `locales`, `internal/format` | Message catalogs and locale-aware number formatting |

The app icon is `AppIcon.icon`, an Icon Composer document (`macos.icon` in
`mygo.json`). `mygo build` compiles it with Xcode 26 or later.

### Adding an engine

1. Create a package under `internal/engines/` with a type that implements
   `engine.Engine`. Build its `engine.Info` once in `New`.
2. Give it an `engine.Installer`. An engine with a Homebrew formula uses
   `enginekit.Homebrew` with its formula, size, executable paths
   (`enginekit.StdBins`), a version reader and a release feed
   (`enginekit.GitHubLatest`, `enginekit.FormulaLatest` or
   `enginekit.TapFormulaLatest`). An engine
   installed another way (pip or uv, a downloaded binary) implements its
   six methods itself and says what it needs first in
   `InstallInfo.Prerequisite`, with a new `engine.InstallMethod`. The
   supervisor needs no change for it, but the install, update and engine
   switch texts of the panel and the catalogs name Homebrew today: a new
   method needs its own wording there.
3. Take the shared parts from `enginekit`: `Request` and `LocalClient` for
   the server's HTTP API, `ParseProm` for its metrics, `Environ` for the
   child environment, `MustFile` for embedded logos, `ChatURL`.
4. Download models apart from the server: implement `engine.Downloader`
   (`Download` with progress, `DropPartial`). A cancelled `Download` keeps
   what it fetched for a resume (the core queues, pauses and persists the
   jobs); `DropPartial` runs only when the user removes a job, and only while
   no other download of the engine runs. Splash runs its own model
   installer (`install/models.py … prepare`, the step `splash serve` runs
   before it serves); llama.cpp's files are fetched by Lumilo into the
   Hugging Face hub cache in the layout `llama-server -hf` reads, and
   TensorFold's in the layout `tensorfold pull` writes
   (`enginekit.HubDownload`: resume, SHA-256 or Git blob check, atomic
   rename). An
   engine without it downloads on the server's start instead: then
   `WatchDownload` returns a meter of the bytes fetched so far
   (`enginekit.HubMeter` for the hub cache) and `EventDownloading.Bytes` is
   the size it fetches, when the engine says it. An engine that keeps its
   own copy of models can offer the files another engine downloaded:
   `engine.Adopter` lists them, and `Download` of the ID installs one
   (Splash assembles a GGUF of the hub cache).
5. Add it to the engine list in `app.go`.
6. Test it against responses and output recorded from the real server, as
   `internal/engines/splash/testdata`, `internal/engines/llamacpp/testdata`
   and `internal/engines/tensorfold/testdata` do. For an engine installed with Homebrew, the supervisor and the panel
   need no change.

## License

MIT — see [LICENSE](LICENSE). Third-party material is listed in
[THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
