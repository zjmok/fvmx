# fvmx

语言：简体中文 | [English](README-en.md)

`fvmx` 是一个用 Go 实现的轻量 Flutter 版本管理 CLI：用裸仓库共享 Git objects，用 worktree 安装多个 Flutter SDK 版本，并让每个版本拥有独立的 `bin/cache`。

## 能力对比

| 能力 | fvmx | FVM |
| --- | --- | --- |
| Git 存储 | ✅ 裸仓库 + worktree，共享 objects，避免为每个版本重复 clone Git 数据 | ⚠️ 使用缓存仓库 + SDK 目录，Git 数据有共享但目录结构更重 |
| 多 repo 支持 | ✅ repo 是一等概念，原生支持 `origin` / `ohos` 等多个来源 | ⚠️ 支持自定义 fork / Flutter URL，但不是以多 repo 命名空间为核心 |
| 版本粒度 | ✅ 支持 commit / branch / tag，并以 `<repo>@<ref>` 区分来源 | ✅ 支持版本号 / tag，也支持 commit hash |
| 安装性能 | ✅ worktree 复用裸仓库 objects，避免完整 clone；仍需 checkout SDK 文件 | ⚠️ 依赖 clone / cache 机制，成熟但首次安装成本更高 |
| 磁盘占用（Git） | ✅ 单 object store，多版本共享 Git 对象 | ⚠️ 有缓存复用，但多个 SDK 目录仍会带来更高整体占用 |
| SDK 隔离 | ✅ 每个版本独立 worktree + bin/cache | ✅ 每个版本独立目录 |
| 项目绑定 | ✅ `.fvmx/` + `.fvmxrc` | ✅ `.fvm/` + `.fvmrc` |
| 命令转发 | ✅ `fvmx flutter` | ✅ `fvm flutter` |
| 分发方式 | ✅ 单二进制（Go） | ✅ 单二进制（Dart 编译） |
| 跨平台稳定性 | ⚠️ worktree 边界较多（尤其 Windows） | ✅ 更成熟稳定 |
| 生态成熟度 | ❌ 新工具 | ✅ 成熟生态 |

## 进度

- [x] `fvmx repo add <name> <url>`：克隆裸仓库到 `~/.fvmx/repos/<name>.git`，并写入 `config.json`
- [x] `fvmx repo set <name> <url>`：修改已存在 repo 的来源地址，并同步更新裸仓库 remote
- [x] `fvmx repo list`：列出已配置的 repo 名称和来源地址
- [x] `fvmx repo update [name]`：更新一个或全部已配置 repo 的裸仓库
- [x] `fvmx repo remove <name>`：删除裸仓库并从 config 移除 repo；如有已安装版本则拒绝删除
- [x] `fvmx install <repo> <ref>`：拉取裸仓库，解析 commit / branch / tag，然后创建 `~/.fvmx/versions/<repo>@<ref>` worktree
- [x] `fvmx list`：列出已安装的 SDK 版本，并标记当前项目正在使用的版本
- [x] `fvmx use <repo@ref-or-alias>`：在当前项目创建 `.fvmx/flutter_sdk` 指向已安装 SDK，并写入 `.fvmx/version` 与 `.fvmxrc`；支持 alias
- [x] `fvmx flutter [args...]`：转发到当前项目 `.fvmx/flutter_sdk/bin/flutter`
- [x] `fvmx remove <repo@ref-or-alias>`：通过 `git worktree remove` 删除已安装版本；删除前确认提示；支持 alias
- [x] `fvmx alias add <alias> <repo@ref>`：创建指向已安装版本的全局别名
- [x] `fvmx alias list`：列出所有全局别名
- [x] `fvmx alias remove <alias>`：删除全局别名
- [x] 删除确认：`remove` 和 `repo remove` 在操作前通过 `y/Y` 确认
- [x] 进度日志：`repo add`、`repo update`、`install` 输出步骤日志
- [x] 测试覆盖：删除确认、进度日志、alias 完整流程

## 使用方式

可以直接使用项目中预编译好的二进制文件。把 `fvmx` 或 `fvmx.exe` 放到 `PATH` 中的任意目录后，确认命令可用：

```bash
fvmx --help
```

常用命令：

```bash
fvmx repo add ohos <url>
fvmx repo set ohos <new-url>
fvmx repo list
fvmx repo update ohos
fvmx repo remove ohos
fvmx install ohos 3.35
fvmx list
fvmx use ohos@3.35
fvmx flutter --version
fvmx remove ohos@3.35
fvmx alias add ohos_3_35 ohos@3.35
fvmx alias list
fvmx alias remove ohos_3_35
```

## 开发

开发时可以直接用 `go run` 调试当前源码：

```bash
go run ./cmd/fvmx --help
go run ./cmd/fvmx repo list
```

运行测试：

```bash
go test ./...
```

## 编译

需要自己编译时使用以下命令。

Windows：

```powershell
go build -o fvmx.exe ./cmd/fvmx
```

macOS / Linux：

```bash
go build -o fvmx ./cmd/fvmx
```

## 存储结构

全局数据目录默认是 `~/.fvmx`，可通过 `FVMX_HOME` 覆盖：

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

项目目录中的本地状态：

```text
project/
├── .fvmx/
│   ├── flutter_sdk -> ~/.fvmx/versions/ohos@3.35
│   └── version
└── .fvmxrc
```

`.fvmx/version` 存储 fvmx 使用的完整版本 ID：

```text
ohos@3.35
```

`.fvmxrc` 存储面向 Flutter 版本的简洁配置：

```json
{
  "flutter": "3.35"
}
```

## 设计说明

- 安装时使用 `git rev-parse --verify <ref>^{commit}` 统一支持 commit、branch 和 tag。
- 版本目录使用 `<repo>@<ref>`，例如 `ohos@3.35`；`install` 仍会先把 ref 解析成具体 commit，再创建 worktree。
- 如果 ref 包含路径分隔符或其他不适合作为目录名的字符，会被规范化为 `-`。
- `install` 只创建 worktree，不共享、不软链 `bin/cache`，保证每个 SDK 版本的 Flutter cache 独立。
- `fvmx list` 优先读取当前项目的 `.fvmx/version` 来标记当前版本；如果该文件不存在，会尝试从 `.fvmxrc` 的 `flutter` 字段推断。
- Windows 下 `.fvmx/flutter_sdk` 使用目录 junction，减少普通用户创建目录链接时的权限要求。
- `fvmx flutter ...` 会读取当前项目的 `.fvmx/flutter_sdk`，并执行其中的 `bin/flutter`；Windows 下优先使用 `bin/flutter.bat`。
- `remove` 和 `repo remove` 在执行操作前提示 `Type y to confirm:`，只有输入 `y` 或 `Y` 才继续。`repo remove` 如果检测到该 repo 仍有已安装版本会直接拒绝。
- 全局别名存储在 `~/.fvmx/config.json` 的 `aliases` 字段中，只能指向已安装版本，`use` 和 `remove` 命令支持别名解析。
- `repo add`、`repo update`、`install` 会输出步骤日志（如 `Cloning bare repo...`、`Fetching repo...`、`Resolving ref...`），方便了解长耗时命令的进度。

## 后续阶段

- 第二阶段：lazy cache、status
- 第三阶段：GC、CI 支持、多用户共享 cache
