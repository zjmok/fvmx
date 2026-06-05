# AGENTS.md

## 项目概述

`fvmx` 是一个用 Go 实现的轻量 Flutter SDK 版本管理 CLI，基于 Git 裸仓库（bare repo）+ worktree 架构实现多版本并行与高效存储。支持多仓库来源（官方 / ohos 等）、版本安装与切换、别名管理、自升级等功能。

## 技术栈

- **语言**: Go 1.26
- **构建**: GoReleaser（跨平台编译 + GitHub Releases 发布）
- **CI**: GitHub Actions（test.yml 跑测试，release.yml 跑发布）
- **无外部依赖**: 仅使用 Go 标准库

## 项目结构

```
cmd/fvmx/main.go          # 入口，解析 --version/-v，调用 fvmx.Run()
internal/fvmx/app.go      # 核心逻辑：所有子命令实现、配置读写、Git 操作
internal/fvmx/update.go   # update 命令：GitHub API 查询、下载、校验、自替换
internal/fvmx/presets.json # 内嵌预设仓库（origin、ohos）
internal/fvmx/app_test.go # 单元测试
```

## 常用命令

```bash
# 运行
go run ./cmd/fvmx --help
go run ./cmd/fvmx repo list

# 测试
go test ./...

# 编译
go build -o fvmx ./cmd/fvmx          # macOS/Linux
go build -o fvmx.exe ./cmd/fvmx      # Windows
```

## 架构与设计要点

### 入口与错误处理

- `main.go` 通过 `-ldflags "-X main.version=..."` 注入版本号，dev 构建默认为 `"dev"`。
- `fvmx.Run(args, Env)` 是核心入口，返回 `(output, error)`。`ExitError` 携带退出码，main 据此调用 `os.Exit`。
- 退出码约定：0 = 成功/取消/`--check`；1 = 运行时错误；2 = 参数错误/dev 无 `--force`。

### Env 注入

- `Env` 结构体封装所有外部依赖（文件系统、I/O、HTTP、版本号、可执行文件路径），测试时通过注入实现隔离。
- `Run` 内部对空字段填充默认值，测试只需注入关心的字段。

### 存储结构

- 全局目录 `~/.fvmx`（可通过 `FVMX_HOME` 覆盖）：
  - `repos/<name>.git` — 裸仓库（共享 Git objects）
  - `versions/<repo>@<ref>` — worktree（每个版本独立 SDK）
  - `config.json` — 仓库配置 + 别名映射
- 项目目录：
  - `.fvmxrc` — fvmx 命令读取的配置（`flutter` + 可选 `repo`）
  - `.fvmx/flutter_sdk` — 符号链接/junction 指向已安装 SDK
  - `.fvmx/version` — 完整版本 ID（如 `ohos@3.35`）

### 版本解析

- `<repo>@<ref>` 格式，ref 支持 commit / branch / tag。
- `resolveCommit` 用 `git rev-parse --verify <ref>^{commit}` 统一解析。
- `versionLabel` 将非法路径字符替换为 `-`，空结果用 commit 短标识替代。
- 别名存储在 `config.json` 的 `aliases` 字段，`use`/`remove` 支持别名解析。

### 版本查找

- `findInstalledVersion` 先精确匹配目录名，失败后前缀匹配（处理 ref 被规范化的情况）。
- `list`/`flutter`/`dart` 共用解析逻辑：① `.fvmxrc` 有 `repo`+`flutter` 则精确匹配 ② 仅 `flutter` 则扫描 `versions/` 唯一匹配。

### update 自升级

- 从 `zjmok/fvmx` GitHub Releases 下载对应平台的 archive。
- SHA256 校验：下载 release 自带的 `checksums.txt`，与 archive 哈希比对。
- 替换策略：先写 `<exe>.new`，再原子替换（Unix 用 `os.Rename`；Windows 用 `cmd /c` 异步 `move /Y`）。
- `FVMX_UPDATE_API` 环境变量可覆盖 GitHub API base（测试/代理用）。

### Windows 特殊处理

- `.fvmx/flutter_sdk` 使用目录 junction（减少权限要求）。
- update 替换用 `ping` 延迟 + `move /Y` 异步替换，避免文件被占用。

## 代码风格

- 中文注释，函数/变量用英文。
- `logStep(env, msg)` 输出步骤日志，用于长时间命令的进度反馈。
- `confirm(env, prompt)` 交互确认，仅 `y`/`Y` 视为同意。
- 配置写入用原子方式：先写 `.tmp` 再 `os.Rename`。
- `runGit` 封装 Git 命令执行，失败时返回带 stderr 摘要的错误。

## 测试

- 测试文件 `internal/fvmx/app_test.go`，通过注入 `Env` 隔离文件系统和 I/O。
- 运行：`go test ./...`

## 发布流程

1. 打 tag `vX.Y.Z` 推送到 GitHub。
2. GitHub Actions（release.yml）触发 GoReleaser 构建。
3. GoReleaser 编译多平台二进制、打包 archive、生成 checksums.txt、创建 GitHub Release。
