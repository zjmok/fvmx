# fvmx

Language: [简体中文](README.md) | English

`fvmx` is a lightweight Flutter SDK version management CLI written in Go, powered by bare Git repos and worktrees for efficient multi-version management.

## Capability Comparison

Both tools serve a similar purpose; the differences below are design trade-offs and maturity, stated as factually as possible:

| Capability | fvmx | fvm |
| --- | --- | --- |
| Git Object Reuse | Bare repo + worktrees: all versions share a single object store | Bare git cache + cloned version dirs: objects reused via Git alternates |
| Multi-source Support | First-class repos with native `<repo>@<ref>` namespacing (official / ohos side by side) | Forks / custom Flutter URLs |
| Version Granularity | commit / branch / tag | Versions / tags / commit hashes |
| Project Pinning | `.fvmxrc` + `.fvmx/` (with a `flutter_sdk` link for IDEs / CI) | `.fvmrc` + `.fvm/` |
| Command Proxy | `fvmx flutter` / `fvmx dart` | `fvm flutter` / `fvm dart` |
| Global Default Version | ✅ `fvmx global` (link at `~/.fvmx/default`, used via PATH) | ✅ `fvm global` |
| Maturity | New tool; fewer cross-platform edge cases battle-tested | Mature ecosystem, well documented, battle-tested |

### Pros and Cons

**fvmx**

- ✅ Object sharing covers every source: one bare repo per repo, shared even between fork-based versions (measured: two ohos versions share a single 2.0 GB object store; the same setup on fvm costs two independent ~4 GB copies)
- ✅ Worktree has no indirection layer: objects always live inside the bare repo, per-version Git metadata is only 1–2 MB
- ✅ Single binary in pure Go standard library, cross-compilable from any platform
- ⚠️ New tool; ecosystem and cross-platform edge cases are less battle-tested
- ⚠️ After moving the cache directory, worktree pointers need `git worktree repair` (same path coupling as fvm, but with an official repair command)

**fvm**

- ✅ Mature community and rich features: global default version, flavors, fork management, doctor, etc.
- ✅ Its cache mechanism works well for official versions (hardlink mirror clones since 4.1, `--reference` in 4.0.x; measured per-version Git metadata of only 1–20 MB)
- ⚠️ Fork / custom URL versions do not participate in object sharing — each is a full independent copy (measured ~2 GB each)
- ⚠️ Legacy issues from mechanism changes: after cache path migration, old versions' `objects/info/alternates` dangle (measured: 2 broken versions needing manual path fixes); repacking inside a version materializes hardlink sharing into independent data

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
- `fvmx global`: show the global default version; `fvmx global <repo@ref-or-alias>` sets it (must be installed, supports alias); `fvmx global --unlink` removes it. Add `~/.fvmx/default/bin` to PATH to use the global `flutter`/`dart` outside projects
- `fvmx alias add <alias> <repo@ref>`: create a global alias pointing to an installed version
- `fvmx alias list`: list all global aliases
- `fvmx alias remove <alias>`: remove a global alias
- `fvmx releases <repo> [channel]`: list available releases (official repos fetch from Google Storage, others use git ls-remote)
- `fvmx update [version] [--check] [--pre] [--force]`: check and upgrade the `fvmx` binary from GitHub Releases; refuses dev builds by default

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
fvmx global                              # show the global default version
fvmx global ohos@3.35                    # set the global default (supports alias)
fvmx global --unlink                     # remove the global default
fvmx alias add ohos_3_35 ohos@3.35
fvmx alias list
fvmx alias remove ohos_3_35
fvmx releases origin                     # official stable releases
fvmx releases origin beta                # official beta releases
fvmx releases ohos                       # non-official repo tags/branches
fvmx update                              # check and upgrade to the latest stable release
fvmx update --check                      # only check for a newer version, do not download
fvmx update 0.2.0                        # upgrade to a specific version (accepts v0.2.0 / 0.2.0)
fvmx update --pre                        # include prereleases
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

Use the following commands when you need to build the binary yourself. Build artifacts go to `dist/` (gitignored; staging area only — install to PATH manually if you want to run `fvmx` directly). Names follow `fvmx-<os>-<arch>`, with `.exe` for Windows binaries. Filenames carry no version — query it with `fvmx --version`.

Windows:

```powershell
go build -o dist/fvmx-windows-amd64.exe ./cmd/fvmx
```

macOS / Linux:

```bash
go build -o dist/fvmx-darwin-arm64 ./cmd/fvmx
```

Cross-compile other platforms (pure Go standard library, no CGO, works from any host):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/fvmx-linux-amd64 ./cmd/fvmx
```

## Storage Layout

The global data directory defaults to `~/.fvmx`. You can override it with `FVMX_CACHE_PATH`:

```text
~/.fvmx/
├── repos/
│   ├── origin.git/
│   └── ohos.git/
├── versions/
│   ├── origin@stable/
│   └── ohos@3.35/
├── default -> versions/ohos@3.35   # global default version link (for PATH use)
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
- `fvmx list`, `fvmx flutter`, and `fvmx dart` share the same resolution logic: ① read `.fvmxrc` → with `repo` + `flutter` for exact installed version match ② with only `flutter`, scan `versions/` for a unique suffix match. Outside a project (no `.fvmxrc` found), `flutter`/`dart` fall back to the global default set by `fvmx global`; if none is set, they error out.
- `fvmx global` persists state in the `~/.fvmx/default` link itself (mirroring fvm's `~/fvm/default`), not in `config.json`; the version ID is recovered from the link target's directory name. `list` marks the global version in a dedicated Global column.
- On Windows, `.fvmx/flutter_sdk` uses a directory junction to avoid requiring elevated privileges for normal directory symlinks.
- `remove` and `repo remove` prompt with `(y/N)` and only proceed on `y`/`Y` input. `repo remove` blocks deletion if any version from that repo is still installed.
- Aliases are stored globally in `~/.fvmx/config.json` under the `aliases` key. They can only point to already-installed versions and are resolved by `use` and `remove` commands.
- `repo add`, `repo update`, and `install` output step logs (e.g. `Cloning bare repo...`, `Fetching repo...`, `Resolving ref...`) for visibility during long-running operations.
- `repo init` uses an embedded `presets.json` to offer official and ohos presets interactively. Already-configured repos are updated via `repo set`, new repos are added via `repo add`.
- `releases` fetches the release list from Google Storage's `releases_<platform>.json` for official repos (`github.com/flutter/flutter`), with channel defaulting to `stable`. For non-official repos, it uses `git ls-remote` to list tags and branches.
- `update` adds no new dependencies: semver comparison is hand-written (`parseVersion` / `compareVersions` in `update.go`), and archive extraction uses `archive/tar` + `compress/gzip` / `archive/zip` to unpack the `fvmx` binary. Replacement is two-phase — write to `<exe>.new`, then atomically rename. Windows schedules the move via `cmd /c "ping ... & move /Y"` so it runs after the parent exits. Integrity is verified using SHA256 against the release's `checksums.txt`; mismatches abort the replacement. The version is injected into `main.version` via `-ldflags "-X main.version=..."`.

## Roadmap

- **doctor** — SDK health diagnostics: path integrity, binary executability, version consistency
- **GC** — Clean up unreferenced bare repo objects and stale worktrees
- **CI support** — Non-interactive mode, auto `.fvmxrc` → install → use
- **Shell completion** — Dynamic completion for commands, repo names, versions, aliases
