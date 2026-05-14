# fvmx

Language: [简体中文](README.md) | English

`fvmx` is a lightweight Flutter SDK version management CLI written in Go, powered by bare Git repos and worktrees for efficient multi-version management.

## Capability Comparison

| Capability | fvmx | fvm |
| --- | --- | --- |
| Git Storage | ✅ Bare repo + worktree, shared objects, avoids duplicating Git data for every version | ⚠️ Uses a cache repository + SDK directories; Git data is shared, but the layout is heavier |
| Multi-repo Support | ✅ Repos are first-class, with native support for multiple sources such as `origin` and `ohos` | ⚠️ Supports custom forks / Flutter URLs, but not as a multi-repo namespace |
| Version Granularity | ✅ Supports commit / branch / tag, distinguished as `<repo>@<ref>` | ✅ Supports versions / tags, and also commit hashes |
| Install Performance | ✅ Worktrees reuse bare-repo objects and avoid full clones; SDK files still need to be checked out | ⚠️ Uses clone / cache mechanisms; mature, but first installs cost more |
| Disk Usage (Git) | ✅ Single object store shared by multiple versions | ⚠️ Cache reuse exists, but multiple SDK directories still increase total usage |
| SDK Isolation | ✅ Isolated worktree + bin/cache per version | ✅ Isolated per version |
| Project Pinning | ✅ `.fvmx/` + `.fvmxrc` | ✅ `.fvm/` + `.fvmrc` |
| Command Proxy | ✅ `fvmx flutter` | ✅ `fvm flutter` |
| Distribution | ✅ Single binary (Go) | ✅ Single binary (compiled Dart) |
| Cross-platform Stability | ⚠️ More edge cases (especially Windows) | ✅ More mature and stable |
| Ecosystem | ❌ New tool | ✅ Mature ecosystem |

## Features

- `fvmx repo init`: interactively add preset repos (origin / ohos) from embedded presets.json
- `fvmx repo add <name> <url>`: clone a bare repository into `~/.fvmx/repos/<name>.git` and write it to `config.json`
- `fvmx repo set <name> <url>`: change the source URL for an existing repo and update the bare repository remote
- `fvmx repo list`: list configured repo names and source URLs
- `fvmx repo update [name]`: update one configured bare repository, or all of them
- `fvmx repo remove <name>`: delete a bare repository and remove from config; rejects if versions are still installed
- `fvmx install <repo> <ref>`: fetch the bare repository, resolve a commit / branch / tag, then create a `~/.fvmx/versions/<repo>@<ref>` worktree
- `fvmx list`: list installed SDK versions with Flutter/Dart version columns, mark the current project version
- `fvmx use <repo@ref-or-alias>`: create `.fvmx/flutter_sdk` in the current project, then write `.fvmx/version` and `.fvmxrc`; supports alias
- `fvmx flutter [args...]`: resolve the installed SDK from `.fvmxrc` and execute `bin/flutter`
- `fvmx dart [args...]`: resolve the installed SDK from `.fvmxrc` and execute `bin/dart`
- `fvmx remove <repo@ref-or-alias>`: remove an installed version with `git worktree remove`; prompts for confirmation; supports alias
- `fvmx alias add <alias> <repo@ref>`: create a global alias pointing to an installed version
- `fvmx alias list`: list all global aliases
- `fvmx alias remove <alias>`: remove a global alias
- `fvmx releases <repo> [channel]`: list available releases (official repos fetch from Google Storage, others use git ls-remote)

## Usage

Download the binary for your platform from [GitHub Releases](../../releases/latest), then put it in your `PATH`:

```bash
fvmx --help
```

Common commands:

```bash
fvmx --version                           # show version
fvmx repo init                           # interactively add preset repos
fvmx repo add ohos <url>
fvmx repo set ohos <new-url>
fvmx repo list
fvmx repo update ohos
fvmx repo remove ohos
fvmx install ohos 3.35
fvmx list                                # table with Flutter/Dart version, aliases
fvmx use ohos@3.35
fvmx flutter --version
fvmx dart --version                      # Dart command forwarding
fvmx remove ohos@3.35
fvmx alias add ohos_3_35 ohos@3.35
fvmx alias list
fvmx alias remove ohos_3_35
fvmx releases origin                     # official stable releases
fvmx releases origin beta                # official beta releases
fvmx releases ohos                       # non-official repo tags/branches
```

## Development

During development, you can run the current source directly with `go run`:

```bash
go run ./cmd/fvmx --help
go run ./cmd/fvmx repo list
```

Run tests:

```bash
go test ./...
```

## Build

Use the following commands when you need to build the binary yourself.

Windows:

```powershell
go build -o fvmx.exe ./cmd/fvmx
```

macOS / Linux:

```bash
go build -o fvmx ./cmd/fvmx
```

## Storage Layout

The global data directory defaults to `~/.fvmx`. You can override it with `FVMX_HOME`:

```text
~/.fvmx/
├── repos/
│   ├── origin.git/
│   └── ohos.git/
├── versions/
│   ├── origin@stable/
│   └── ohos@3.35/
└── config.json   # { "repos": {...}, "aliases": {...} }
```

Project-local state:

```text
project/
├── .fvmx/
│   ├── flutter_sdk -> ~/.fvmx/versions/ohos@3.35
│   └── version
└── .fvmxrc
```

`.fvmx/version` stores the full version ID used by fvmx:

```text
ohos@3.35
```

`.fvmxrc` stores the Flutter SDK version configuration:

```json
{
  "flutter": "3.35",
  "repo": "ohos"
}
```

The `repo` field is optional and enables exact version matching, avoiding ambiguity when multiple repos share the same version number.

## Design Notes

- `install` uses `git rev-parse --verify <ref>^{commit}` to support commits, branches, and tags through one path.
- Version directories use `<repo>@<ref>`, for example `ohos@3.35`; `install` still resolves the ref to a concrete commit before creating the worktree.
- If a ref contains path separators or other characters unsuitable for directory names, they are normalized to `-`.
- `install` only creates a worktree. It does not share or symlink `bin/cache`, so each Flutter SDK version keeps its own cache.
- `fvmx` commands only read `.fvmxrc` (the project root config file), never `.fvmx/` directly. The `.fvmx/` directory (containing `flutter_sdk` symlink and `version`) is created by `fvmx use` for IDE / script / CI use.
- `fvmx list`, `fvmx flutter`, and `fvmx dart` share the same resolution logic: ① read `.fvmxrc` → with `repo` + `flutter` for exact installed version match ② with only `flutter`, scan `versions/` for a unique suffix match. `fvmx list` then maps the SDK path back to a version ID.
- On Windows, `.fvmx/flutter_sdk` uses a directory junction to avoid requiring elevated privileges for normal directory symlinks.
- `remove` and `repo remove` prompt with `(y/N)` and only proceed on `y`/`Y` input. `repo remove` blocks deletion if any version from that repo is still installed.
- Aliases are stored globally in `~/.fvmx/config.json` under the `aliases` key. They can only point to already-installed versions and are resolved by `use` and `remove` commands.
- `repo add`, `repo update`, and `install` output step logs (e.g. `Cloning bare repo...`, `Fetching repo...`, `Resolving ref...`) for visibility during long-running operations.
- `repo init` uses an embedded `presets.json` to offer official and ohos presets interactively. Already-configured repos are updated via `repo set`, new repos are added via `repo add`.
- `releases` fetches the release list from Google Storage's `releases_<platform>.json` for official repos (`github.com/flutter/flutter`), with channel defaulting to `stable`. For non-official repos, it uses `git ls-remote` to list tags and branches.

## Roadmap

- **doctor** — SDK health diagnostics: path integrity, binary executability, version consistency
- **GC** — Clean up unreferenced bare repo objects and stale worktrees
- **CI support** — Non-interactive mode, auto `.fvmxrc` → install → use
- **Shell completion** — Dynamic completion for commands, repo names, versions, aliases
