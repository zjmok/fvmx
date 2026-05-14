# fvmx

Language: [简体中文](README.md) | English

`fvmx` is a lightweight Flutter version manager written in Go. It uses bare Git repositories to share Git objects, Git worktrees to install multiple Flutter SDK versions, and keeps each version's `bin/cache` isolated.

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

## Phase 1 Progress

- [x] `fvmx repo add <name> <url>`: clone a bare repository into `~/.fvmx/repos/<name>.git` and write it to `config.json`
- [x] `fvmx repo set <name> <url>`: change the source URL for an existing repo and update the bare repository remote
- [x] `fvmx repo list`: list configured repo names and source URLs
- [x] `fvmx repo update [name]`: update one configured bare repository, or all of them
- [x] `fvmx install <repo> <ref>`: fetch the bare repository, resolve a commit / branch / tag, then create a `~/.fvmx/versions/<repo>@<ref>` worktree
- [x] `fvmx list`: list installed SDK versions and mark the version used by the current project
- [x] `fvmx use <repo@ref>`: create `.fvmx/flutter_sdk` in the current project, then write `.fvmx/version` and `.fvmxrc`
- [x] `fvmx flutter [args...]`: forward commands to the current project's `.fvmx/flutter_sdk/bin/flutter`
- [x] `fvmx remove <repo@ref>`: remove an installed version with `git worktree remove`
- [x] Basic tests: cover the full local Git flow for Phase 1

## Usage

You can use the prebuilt binary included with the project. Put `fvmx` or `fvmx.exe` in any directory on your `PATH`, then confirm it is available:

```bash
fvmx --help
```

Common commands:

```bash
fvmx repo add ohos <url>
fvmx repo set ohos <new-url>
fvmx repo list
fvmx repo update ohos
fvmx install ohos 3.35
fvmx list
fvmx use ohos@3.35
fvmx flutter --version
fvmx remove ohos@3.35
```

## Development

During development, you can run the current source directly with `go run`:

```bash
go run ./cmd/fvmx --help
go run ./cmd/fvmx repo list
go run ./cmd/fvmx repo set ohos <new-url>
go run ./cmd/fvmx repo update ohos
go run ./cmd/fvmx list
go run ./cmd/fvmx use ohos@3.35
go run ./cmd/fvmx flutter --version
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
└── config.json
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

`.fvmxrc` stores a concise Flutter-version-oriented config:

```json
{
  "flutter": "3.35"
}
```

## Design Notes

- `install` uses `git rev-parse --verify <ref>^{commit}` to support commits, branches, and tags through one path.
- Version directories use `<repo>@<ref>`, for example `ohos@3.35`; `install` still resolves the ref to a concrete commit before creating the worktree.
- If a ref contains path separators or other characters unsuitable for directory names, they are normalized to `-`.
- `install` only creates a worktree. It does not share or symlink `bin/cache`, so each Flutter SDK version keeps its own cache.
- `fvmx list` first reads the current project's `.fvmx/version` to mark the active version. If that file does not exist, it tries to infer the version from the `flutter` field in `.fvmxrc`.
- On Windows, `.fvmx/flutter_sdk` uses a directory junction to avoid requiring elevated privileges for normal directory symlinks.
- `fvmx flutter ...` reads the current project's `.fvmx/flutter_sdk` and executes its `bin/flutter`; on Windows it prefers `bin/flutter.bat`.

## Later Phases

- Phase 2: alias, lazy cache, status
- Phase 3: GC, CI support, shared cache for multiple users
