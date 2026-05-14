package fvmx

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

//go:embed presets.json
var presetsData []byte

// preset 表示 presets.json 中一个预置仓库选项
type preset struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// presetsFile 对应 presets.json 的顶层结构
type presetsFile struct {
	Presets []preset `json:"presets"`
}

const configFile = "config.json"              // 全局配置文件
const projectStateDir = ".fvmx"               // 项目本地状态目录（IDE/CI 使用）
const projectConfigFile = ".fvmxrc"           // 项目根配置文件（fvmx 命令使用的配置源）

// repo 名称允许的字符集：字母、数字、点、下划线、连字符
var repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ExitError 定义了 CLI 退出码错误，main 函数根据 Code 决定 os.Exit 值
type ExitError struct {
	Message string
	Code    int
}

func (e *ExitError) Error() string {
	return e.Message
}

// Env 是命令执行上下文，测试时通过注入这些字段来隔离文件系统和 I/O
type Env struct {
	Home   string        // 全局数据目录（~/.fvmx 或 FVMX_HOME）
	Cwd    string        // 当前工作目录
	Stdin  io.Reader     // 用户输入（测试时可注入）
	Stdout io.Writer     // 标准输出
	Stderr io.Writer     // 错误输出
}

// confirm 向用户输出 prompt，等待 y/Y 输入。其他输入视为拒绝。
func confirm(env Env, prompt string) bool {
	fmt.Fprint(env.Stdout, prompt)
	scanner := bufio.NewScanner(env.Stdin)
	if !scanner.Scan() {
		return false
	}
	response := strings.TrimSpace(scanner.Text())
	return response == "y" || response == "Y"
}

// logStep 输出步骤日志到 Stdout，用于 long-running 命令的进度反馈
func logStep(env Env, message string) {
	fmt.Fprintln(env.Stdout, message)
}

// config 对应 ~/.fvmx/config.json 的结构
type config struct {
	Repos   map[string]string `json:"repos"`   // 仓库名 → 远程 URL
	Aliases map[string]string `json:"aliases"` // 别名 → repo@ref
}

// projectConfig 对应项目根目录 .fvmxrc 的结构
type projectConfig struct {
	Flutter string `json:"flutter"`          // ref/版本号，如 "3.35"
	Repo    string `json:"repo,omitempty"`   // 仓库名，如 "ohos"，可省略
}

// versionSpec 是解析后的 <repo>@<ref> 结构
type versionSpec struct {
	Repo  string // 仓库名，如 "ohos"
	Label string // ref/版本号，如 "3.35"
}

// installedVersion 表示已安装的版本，含完整 ID 和路径
type installedVersion struct {
	ID   string // 完整版本 ID，如 "ohos@3.35"
	Path string // 在磁盘上的绝对路径
}

// Run 是 CLI 入口，解析 args 并将命令分派到对应的处理函数。
// 返回值 output 由 main 函数打印到 stdout，err 转换为退出码。两个都非空时先打印 output 再退出。
func Run(args []string, env Env) (string, error) {
	// 用默认值填充 Env 中的空字段，方便测试时只注入需要的字段
	if env.Home == "" {
		env.Home = defaultHome()
	}
	if env.Cwd == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		env.Cwd = cwd
	}
	if env.Stdin == nil {
		env.Stdin = os.Stdin
	}
	if env.Stdout == nil {
		env.Stdout = os.Stdout
	}
	if env.Stderr == nil {
		env.Stderr = os.Stderr
	}

	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		return usage(), nil
	}

	switch args[0] {
	case "repo":
		return repoCommand(env.Home, args[1:], env)
	case "install":
		if len(args) != 3 {
			return "", usageError("install requires <repo> and <ref>.")
		}
		return install(env.Home, args[1], args[2], env)
	case "list":
		if len(args) != 1 {
			return "", usageError("list does not accept arguments.")
		}
		projectRoot := findProjectRoot(env.Cwd)
		return listVersions(env.Home, projectRoot, env)
	case "use":
		if len(args) != 2 {
			return "", usageError("use requires <repo@ref-or-alias>.")
		}
		// 参考 FVM 行为：非 Flutter 项目目录使用前需要用户确认
		if _, err := os.Stat(filepath.Join(env.Cwd, "pubspec.yaml")); errors.Is(err, os.ErrNotExist) {
			if !confirm(env, "No pubspec.yaml detected in this directory. Would you like to continue? (y/N) ") {
				return "Cancelled.", nil
			}
		}
		return useVersion(env.Home, args[1], env.Cwd)
	case "flutter":
		if err := runFlutter(args[1:], env); err != nil {
			return "", err
		}
		return "", nil
	case "dart":
		if err := runDart(args[1:], env); err != nil {
			return "", err
		}
		return "", nil
	case "remove":
		if len(args) != 2 {
			return "", usageError("remove requires <repo@ref-or-alias>.")
		}
		return removeVersion(env.Home, args[1], env)
	case "alias":
		return aliasCommand(env.Home, args[1:])
	case "releases":
		return releasesCommand(env.Home, args[1:])
	default:
		return "", usageError(fmt.Sprintf("unknown command: %s", args[0]))
	}
}

func usage() string {
	return `Usage:
  fvmx repo init
  fvmx repo add <name> <url>
  fvmx repo set <name> <url>
  fvmx repo list
  fvmx repo update [name]
  fvmx repo remove <name>
  fvmx install <repo> <ref>
  fvmx list
  fvmx use <repo@ref-or-alias>
  fvmx remove <repo@ref-or-alias>
  fvmx alias add <alias> <repo@ref>
  fvmx alias list
  fvmx alias remove <alias>
  fvmx releases <repo> [channel]
  fvmx flutter [args...]
  fvmx dart [args...]

Environment:
  FVMX_HOME  Override the storage directory (default: ~/.fvmx)`
}

func usageError(message string) error {
	return &ExitError{Message: message + "\n\n" + usage(), Code: 2}
}

// defaultHome 获取 fvmx 全局数据目录。优先读取 FVMX_HOME 环境变量，否则使用 ~/.fvmx
func defaultHome() string {
	if home := os.Getenv("FVMX_HOME"); home != "" {
		return filepath.Clean(home)
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return ".fvmx"
	}
	return filepath.Join(userHome, ".fvmx")
}

// ensureBaseDirs 创建全局数据目录下的 repos/ 和 versions/ 子目录
func ensureBaseDirs(home string) error {
	if err := os.MkdirAll(filepath.Join(home, "repos"), 0o755); err != nil {
		return err
	}
	return os.MkdirAll(versionsDir(home), 0o755)
}

// repoPath 返回裸仓库的路径，格式为 <home>/repos/<name>.git
func repoPath(home, name string) string {
	return filepath.Join(home, "repos", name+".git")
}

// versionsDir 返回版本安装目录 <home>/versions
func versionsDir(home string) string {
	return filepath.Join(home, "versions")
}

// readConfig 从 <home>/config.json 读取配置。文件不存在时返回空配置，不会报错。
func readConfig(home string) (config, error) {
	path := filepath.Join(home, configFile)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return config{Repos: map[string]string{}, Aliases: map[string]string{}}, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return config{}, err
	}

	var cfg config
	if err := json.Unmarshal(content, &cfg); err != nil {
		return config{}, fmt.Errorf("invalid config file at %s: %w", path, err)
	}
	if cfg.Repos == nil {
		cfg.Repos = map[string]string{}
	}
	if cfg.Aliases == nil {
		cfg.Aliases = map[string]string{}
	}
	return cfg, nil
}

// writeConfig 以原子写入方式（先写 .tmp 再 rename）保存配置到 config.json
func writeConfig(home string, cfg config) error {
	if err := ensureBaseDirs(home); err != nil {
		return err
	}

	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(home, configFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(content, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// runGit 执行 git 命令，返回 stdout 内容。命令失败时返回带 stderr 摘要的错误。
func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), detail)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// assertRepoName 验证 repo 名称只包含允许的字符
func assertRepoName(name string) error {
	if !repoNamePattern.MatchString(name) {
		return &ExitError{
			Message: "repo name may only contain letters, numbers, dots, underscores, and hyphens",
			Code:    1,
		}
	}
	return nil
}

// parseVersionSpec 解析 "<repo>@<ref>" 格式的版本标识符
// 入参: spec - "ohos@3.35" 格式的版本标识符
// 出参: versionSpec - 解析后的结构体，Repo 和 Label 分别对应仓库名和版本号
func parseVersionSpec(spec string) (versionSpec, error) {
	index := strings.Index(spec, "@")
	if index <= 0 || index == len(spec)-1 {
		return versionSpec{}, &ExitError{
			Message: "version must use <repo>@<ref>, for example: ohos@3.35",
			Code:    1,
		}
	}

	parsed := versionSpec{Repo: spec[:index], Label: spec[index+1:]}
	if err := assertRepoName(parsed.Repo); err != nil {
		return versionSpec{}, err
	}
	return parsed, nil
}

// resolveCommit 将 ref（commit hash / branch / tag）解析为完整 commit hash
// 使用 git rev-parse --verify <ref>^{commit} 统一支持三种 ref 类型
func resolveCommit(repoPath, ref string) (string, error) {
	return runGit("--git-dir", repoPath, "rev-parse", "--verify", ref+"^{commit}")
}

// shortCommit 截取 commit hash 前 12 位作为短标识
func shortCommit(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}

// versionLabel 从 ref 生成适合作为目录名的版本标签
// 将非法路径字符替换为连字符，若结果为空则用 commit hash 短标识替代
func versionLabel(ref string, commit string) string {
	label := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(ref, "-")
	label = strings.Trim(label, "-.")
	if label == "" {
		return shortCommit(commit)
	}
	return label
}

// resolveVersionOrAlias 将输入解析为已安装版本。
// 输入含 @ 时按版本 ID 解析，否则先查别名映射，找不到则报错。
func resolveVersionOrAlias(home, input string) (installedVersion, error) {
	if strings.Contains(input, "@") {
		return findInstalledVersion(home, input)
	}
	cfg, err := readConfig(home)
	if err != nil {
		return installedVersion{}, err
	}
	if target, ok := cfg.Aliases[input]; ok {
		return findInstalledVersion(home, target)
	}
	return installedVersion{}, &ExitError{Message: "unknown version or alias: " + input, Code: 1}
}

// findInstalledVersion 在 versions/ 目录中查找已安装的版本。
// 优先精确匹配 <repo>@<ref>，失败后通过前缀匹配（用于 ref 被规范化后的版本）。
func findInstalledVersion(home, spec string) (installedVersion, error) {
	parsed, err := parseVersionSpec(spec)
	if err != nil {
		return installedVersion{}, err
	}

	exactID := parsed.Repo + "@" + parsed.Label
	exactPath := filepath.Join(versionsDir(home), exactID)
	if info, err := os.Stat(exactPath); err == nil && info.IsDir() {
		return installedVersion{ID: exactID, Path: exactPath}, nil
	}

	entries, err := os.ReadDir(versionsDir(home))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return installedVersion{}, &ExitError{Message: "version is not installed: " + spec, Code: 1}
		}
		return installedVersion{}, err
	}

	// 前缀匹配：处理 ref 被 versionLabel 规范化后的目录名
	prefix := parsed.Repo + "@" + parsed.Label
	matches := []string{}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			matches = append(matches, entry.Name())
		}
	}

	if len(matches) == 0 {
		return installedVersion{}, &ExitError{Message: "version is not installed: " + spec, Code: 1}
	}
	if len(matches) > 1 {
		return installedVersion{}, &ExitError{Message: "version is ambiguous: " + spec + ". Matches: " + strings.Join(matches, ", "), Code: 1}
	}

	return installedVersion{ID: matches[0], Path: filepath.Join(versionsDir(home), matches[0])}, nil
}

// repoInit 交互式选择 presets 中的仓库进行添加或更新。
// 对每个选中项：已存在则调 repoSet，不存在则调 repoAdd。
func repoInit(home string, env Env) (string, error) {
	var pf presetsFile
	if err := json.Unmarshal(presetsData, &pf); err != nil {
		return "", err
	}

	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}

	fmt.Fprintln(env.Stdout, "Available presets:")
	for i, p := range pf.Presets {
		fmt.Fprintf(env.Stdout, "  %d. %s    %s\n", i+1, p.Name, p.Description)
	}
	fmt.Fprint(env.Stdout, "Select presets to add (e.g. 1,2): ")
	scanner := bufio.NewScanner(env.Stdin)
	if !scanner.Scan() {
		return "Cancelled.", nil
	}
	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		return "Cancelled.", nil
	}

	var results []string
	selections := strings.Split(input, ",")
	for _, s := range selections {
		s = strings.TrimSpace(s)
		var idx int
		if _, err := fmt.Sscanf(s, "%d", &idx); err != nil || idx < 1 || idx > len(pf.Presets) {
			results = append(results, fmt.Sprintf("invalid selection: %s", s))
			continue
		}
		p := pf.Presets[idx-1]
		if _, exists := cfg.Repos[p.Name]; exists {
			if _, err := repoSet(home, p.Name, p.URL); err != nil {
				results = append(results, fmt.Sprintf("%s: %v", p.Name, err))
			} else {
				results = append(results, fmt.Sprintf("Updated repo %s: %s", p.Name, p.URL))
			}
		} else {
			if _, err := repoAdd(home, p.Name, p.URL, env); err != nil {
				results = append(results, fmt.Sprintf("%s: %v", p.Name, err))
			} else {
				results = append(results, fmt.Sprintf("Added repo %s: %s", p.Name, p.URL))
			}
		}
	}

	return strings.Join(results, "\n"), nil
}

// repoCommand 是 repo 子命令的调度入口
func repoCommand(home string, args []string, env Env) (string, error) {
	if len(args) == 0 {
		return "", usageError("repo requires a subcommand.")
	}

	switch args[0] {
	case "init":
		if len(args) != 1 {
			return "", usageError("repo init does not accept arguments.")
		}
		return repoInit(home, env)
	case "add":
		if len(args) != 3 {
			return "", usageError("repo add requires <name> and <url>.")
		}
		return repoAdd(home, args[1], args[2], env)
	case "set":
		if len(args) != 3 {
			return "", usageError("repo set requires <name> and <url>.")
		}
		return repoSet(home, args[1], args[2])
	case "list":
		if len(args) != 1 {
			return "", usageError("repo list does not accept arguments.")
		}
		return repoList(home)
	case "update":
		if len(args) > 2 {
			return "", usageError("repo update accepts at most one optional <name>.")
		}
		name := ""
		if len(args) == 2 {
			name = args[1]
		}
		return repoUpdate(home, name, env)
	case "remove":
		if len(args) != 2 {
			return "", usageError("repo remove requires <name>.")
		}
		return repoRemove(home, args[1], env)
	default:
		return "", usageError("unknown repo command: " + args[0])
	}
}

// repoAdd 克隆裸仓库到 repos/<name>.git 并写入 config.json
func repoAdd(home, name, url string, env Env) (string, error) {
	if err := assertRepoName(name); err != nil {
		return "", err
	}
	if err := ensureBaseDirs(home); err != nil {
		return "", err
	}

	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}

	target := repoPath(home, name)
	if _, exists := cfg.Repos[name]; exists {
		return "", &ExitError{Message: "repo already exists: " + name, Code: 1}
	}
	if _, err := os.Stat(target); err == nil {
		return "", &ExitError{Message: "repo already exists: " + name, Code: 1}
	}

	logStep(env, "Cloning bare repo "+name+"...")
	if _, err := runGit("clone", "--bare", url, target); err != nil {
		return "", err
	}

	logStep(env, "Writing config...")
	cfg.Repos[name] = url
	if err := writeConfig(home, cfg); err != nil {
		return "", err
	}

	return fmt.Sprintf("Added repo %s: %s", name, url), nil
}

// repoSet 修改已存在 repo 的远程 URL，同步更新裸仓库的 remote 和 config.json
func repoSet(home, name, url string) (string, error) {
	if err := assertRepoName(name); err != nil {
		return "", err
	}

	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}
	if _, exists := cfg.Repos[name]; !exists {
		return "", &ExitError{Message: "repo does not exist: " + name, Code: 1}
	}

	repo := repoPath(home, name)
	if _, err := os.Stat(repo); err != nil {
		return "", &ExitError{Message: "bare repository is missing: " + repo, Code: 1}
	}

	if _, err := runGit("--git-dir", repo, "remote", "set-url", "origin", url); err != nil {
		return "", err
	}

	cfg.Repos[name] = url
	if err := writeConfig(home, cfg); err != nil {
		return "", err
	}

	return fmt.Sprintf("Updated repo %s: %s", name, url), nil
}

// repoList 按名称排序列出已配置的仓库
func repoList(home string) (string, error) {
	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}

	if len(cfg.Repos) == 0 {
		return "No repos configured.", nil
	}

	names := make([]string, 0, len(cfg.Repos))
	for name := range cfg.Repos {
		names = append(names, name)
	}
	sort.Strings(names)

	var builder strings.Builder
	builder.WriteString("Configured repos:")
	for _, name := range names {
		builder.WriteString("\n  ")
		builder.WriteString(name)
		builder.WriteString("  ")
		builder.WriteString(cfg.Repos[name])
	}
	return builder.String(), nil
}

// repoRemove 删除裸仓库并从 config 中移除仓库条目。
// 如果该 repo 仍有已安装版本则拒绝删除，防止残留 worktree 引用。
func repoRemove(home, name string, env Env) (string, error) {
	if err := assertRepoName(name); err != nil {
		return "", err
	}

	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}
	if _, exists := cfg.Repos[name]; !exists {
		return "", &ExitError{Message: "repo does not exist: " + name, Code: 1}
	}

	// 检查是否有已安装版本引用了该 repo 的 worktree，有则拒绝
	entries, err := os.ReadDir(versionsDir(home))
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), name+"@") {
				return "", &ExitError{
					Message: fmt.Sprintf("cannot remove repo %s: version %s is still installed. Remove it first with: fvmx remove %s", name, entry.Name(), entry.Name()),
					Code:    1,
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	if !confirm(env, fmt.Sprintf("Remove repo %s? (y/N) ", name)) {
		return "Cancelled.", nil
	}

	repo := repoPath(home, name)
	if err := os.RemoveAll(repo); err != nil {
		return "", err
	}

	delete(cfg.Repos, name)
	if err := writeConfig(home, cfg); err != nil {
		return "", err
	}

	return "Removed repo: " + name, nil
}

// repoUpdate 更新一个或全部已配置仓库的远程引用。
// 无参数时更新全部，否则只更新指定仓库。
func repoUpdate(home, name string, env Env) (string, error) {
	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}

	if name != "" {
		if err := assertRepoName(name); err != nil {
			return "", err
		}
		if _, exists := cfg.Repos[name]; !exists {
			return "", &ExitError{Message: "repo does not exist: " + name, Code: 1}
		}
		logStep(env, "Fetching repo "+name+"...")
		if err := repoFetch(home, name); err != nil {
			return "", err
		}
		return "Updated repo: " + name, nil
	}

	if len(cfg.Repos) == 0 {
		return "No repos configured.", nil
	}

	names := make([]string, 0, len(cfg.Repos))
	for repoName := range cfg.Repos {
		names = append(names, repoName)
	}
	sort.Strings(names)

	for _, repoName := range names {
		logStep(env, "Fetching repo "+repoName+"...")
		if err := repoFetch(home, repoName); err != nil {
			return "", err
		}
	}

	return "Updated repos: " + strings.Join(names, ", "), nil
}

// repoFetch 执行 git fetch --all --tags 更新指定仓库的远程引用
func repoFetch(home, name string) error {
	repo := repoPath(home, name)
	if _, err := os.Stat(repo); err != nil {
		return &ExitError{Message: "bare repository is missing: " + repo, Code: 1}
	}
	_, err := runGit("--git-dir", repo, "fetch", "--all", "--tags")
	return err
}

// install 从裸仓库创建 worktree 安装指定版本。
// 步骤：拉取远程引用 → 解析 ref 为 commit → 创建 <repo>@<label> 目录 → checkout commit。
func install(home, name, ref string, env Env) (string, error) {
	if err := assertRepoName(name); err != nil {
		return "", err
	}
	if err := ensureBaseDirs(home); err != nil {
		return "", err
	}

	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}
	if _, exists := cfg.Repos[name]; !exists {
		return "", &ExitError{Message: fmt.Sprintf("unknown repo: %s. Add it first with: fvmx repo add %s <url>", name, name), Code: 1}
	}

	logStep(env, "Fetching repo "+name+"...")
	if err := repoFetch(home, name); err != nil {
		return "", err
	}

	repo := repoPath(home, name)
	logStep(env, "Resolving ref "+ref+"...")
	commit, err := resolveCommit(repo, ref)
	if err != nil {
		return "", err
	}

	id := name + "@" + versionLabel(ref, commit)
	target := filepath.Join(versionsDir(home), id)
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return fmt.Sprintf("Version already installed: %s\n%s", id, target), nil
	}

	logStep(env, "Creating worktree "+id+"...")
	if _, err := runGit("--git-dir", repo, "worktree", "add", target, commit); err != nil {
		return "", err
	}

	return fmt.Sprintf("Installed %s\n%s", id, target), nil
}

// listVersions 列出所有已安装版本，以表格形式展示版本号、Flutter/Dart 版本、别名。
// 当前项目绑定版本用绿色 * 标记。
func listVersions(home, projectDir string, env Env) (string, error) {
	entries, err := os.ReadDir(versionsDir(home))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "No versions installed.", nil
		}
		return "", err
	}

	versions := []string{}
	for _, entry := range entries {
		if entry.IsDir() && strings.Contains(entry.Name(), "@") {
			versions = append(versions, entry.Name())
		}
	}

	if len(versions) == 0 {
		return "No versions installed.", nil
	}

	sort.Strings(versions)
	current := currentProjectVersion(projectDir, versions, home)

	// 构建版本→别名列表的反向映射
	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}
	aliasMap := map[string][]string{}
	for alias, target := range cfg.Aliases {
		aliasMap[target] = append(aliasMap[target], alias)
	}
	for target := range aliasMap {
		sort.Strings(aliasMap[target])
	}

	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 3, ' ', 0)

	if current != "" {
		fmt.Fprintf(tw, "Current project: %s\n", current)
	}
	fmt.Fprintln(tw, "   Version\tFlutter\tDart\tAliases")
	for _, version := range versions {
		marker := "  "
		if version == current {
			marker = " *"
		}
		versionPath := filepath.Join(versionsDir(home), version)
		flutterVer, dartVer := getSDKVersionInfo(versionPath)
		aliases := "-"
		if als, ok := aliasMap[version]; ok && len(als) > 0 {
			aliases = strings.Join(als, ", ")
		}
		fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\n", marker, version, flutterVer, dartVer, aliases)
	}
	tw.Flush()
	return strings.Replace(buf.String(), " * ", " \033[32m*\033[0m ", 1), nil
}

// getSDKVersionInfo 获取 SDK 的 Flutter 和 Dart 版本号。
// 优先读取 version 文件（快路径），缺失时通过执行 flutter --version 回退获取。
// 回退命令有 5 秒超时，防止 flutter 首次运行卡住。
func getSDKVersionInfo(sdkPath string) (flutterVer, dartVer string) {
	if content, err := os.ReadFile(filepath.Join(sdkPath, "version")); err == nil {
		flutterVer = strings.TrimSpace(string(content))
	}
	if content, err := os.ReadFile(filepath.Join(sdkPath, "bin", "cache", "dart-sdk", "version")); err == nil {
		dartVer = strings.TrimSpace(string(content))
	}
	if flutterVer != "" && dartVer != "" {
		return
	}

	toolPath, err := flutterExecutable(sdkPath)
	if err != nil {
		if flutterVer == "" {
			flutterVer = "-"
		}
		if dartVer == "" {
			dartVer = "-"
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" && strings.HasSuffix(strings.ToLower(toolPath), ".bat") {
		cmd = exec.CommandContext(ctx, "cmd", "/c", toolPath, "--version")
	} else {
		cmd = exec.CommandContext(ctx, toolPath, "--version")
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		if flutterVer == "" {
			flutterVer = "-"
		}
		if dartVer == "" {
			dartVer = "-"
		}
		return
	}

	fv, dv := parseFlutterVersionOutput(stdout.String())
	if flutterVer == "" && fv != "" {
		flutterVer = fv
	}
	if dartVer == "" && dv != "" {
		dartVer = dv
	}
	if flutterVer == "" {
		flutterVer = "-"
	}
	if dartVer == "" {
		dartVer = "-"
	}
	return
}

// parseFlutterVersionOutput 从 flutter --version 文本输出中提取 Flutter 和 Dart 版本号。
// 解析规则：匹配 "Flutter X.Y.Z" 行和 "Dart X.Y.Z" 行，兼容 • 和 - 两种分隔符。
func parseFlutterVersionOutput(output string) (flutterVer, dartVer string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if flutterVer == "" && strings.HasPrefix(line, "Flutter ") {
			if parts := strings.Fields(line); len(parts) >= 2 {
				flutterVer = parts[1]
			}
		}
		if dartVer == "" && strings.Contains(line, "Dart ") {
			if idx := strings.Index(line, "Dart "); idx >= 0 {
				rest := line[idx+5:]
				if parts := strings.Fields(rest); len(parts) >= 1 {
					dartVer = parts[0]
				}
			}
		}
		if flutterVer != "" && dartVer != "" {
			break
		}
	}
	return
}

// currentProjectVersion 使用 resolveSDKPath 的相同逻辑（.fvmxrc 优先）确定当前项目绑定的版本 ID。
// 与 runFlutter/runDart 的 SDK 解析逻辑保持一致。
func currentProjectVersion(projectDir string, installed []string, home string) string {
	sdkPath, err := resolveSDKPath(projectDir, home)
	if err != nil {
		return ""
	}
	for _, version := range installed {
		if filepath.Join(versionsDir(home), version) == sdkPath {
			return version
		}
	}
	return ""
}

// findProjectRoot 从 cwd 向上遍历，找到包含 .fvmxrc 的目录作为项目根
func findProjectRoot(cwd string) string {
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, projectConfigFile)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// useVersion 将当前目录绑定到指定版本。
// 创建 .fvmx/flutter_sdk symlink（供 IDE/CI 使用）、.fvmx/version（完整版本 ID）、.fvmxrc（配置）。
func useVersion(home, spec, projectDir string) (string, error) {
	version, err := resolveVersionOrAlias(home, spec)
	if err != nil {
		return "", err
	}

	stateDir := filepath.Join(projectDir, projectStateDir)
	linkPath := filepath.Join(stateDir, "flutter_sdk")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return "", err
	}

	if err := removeExistingLink(linkPath); err != nil {
		return "", err
	}
	if err := createDirLink(version.Path, linkPath); err != nil {
		return "", err
	}
	if err := writeProjectSelection(projectDir, version.ID); err != nil {
		return "", err
	}

	return fmt.Sprintf("Using %s\n%s -> %s", version.ID, linkPath, version.Path), nil
}

// writeProjectSelection 将版本选择写入 .fvmxrc 和 .fvmx/version。
// .fvmxrc 包含 repo 和 flutter 字段，供 fvmx 命令使用。
// .fvmx/version 供 IDE/CI 读取。
func writeProjectSelection(projectDir, versionID string) error {
	stateDir := filepath.Join(projectDir, projectStateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(stateDir, "version"), []byte(versionID+"\n"), 0o644); err != nil {
		return err
	}

	label := versionID
	repo := ""
	if index := strings.Index(versionID, "@"); index >= 0 && index < len(versionID)-1 {
		repo = versionID[:index]
		label = versionID[index+1:]
	}
	content, err := json.MarshalIndent(projectConfig{Flutter: label, Repo: repo}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(projectDir, projectConfigFile), append(content, '\n'), 0o644)
}

// resolveSDKPath 通过 .fvmxrc 解析 SDK 的安装路径。
// 优先使用 repo + flutter 精确匹配，降级为 flutter suffix 扫描，均不匹配则报错。
// 与 currentProjectVersion 共用此逻辑。
func resolveSDKPath(projectRoot, home string) (string, error) {
	rcPath := filepath.Join(projectRoot, projectConfigFile)
	content, err := os.ReadFile(rcPath)
	if err == nil {
		var cfg projectConfig
		if err := json.Unmarshal(content, &cfg); err == nil && cfg.Flutter != "" {
			if cfg.Repo != "" {
				if version, err := findInstalledVersion(home, cfg.Repo+"@"+cfg.Flutter); err == nil {
					return version.Path, nil
				}
			} else {
				entries, err := os.ReadDir(versionsDir(home))
				if err == nil {
					var matches []string
					for _, entry := range entries {
						if entry.IsDir() && strings.HasSuffix(entry.Name(), "@"+cfg.Flutter) {
							matches = append(matches, entry.Name())
						}
					}
					if len(matches) == 1 {
						return filepath.Join(versionsDir(home), matches[0]), nil
					}
				}
			}
		}
	}

	return "", &ExitError{
		Message: "No active Flutter SDK. Run fvmx use <repo@ref> in this project first.",
		Code:    1,
	}
}

// runFlutter 在项目上下文中执行 flutter 命令。
// 通过 resolveSDKPath 找到 SDK 路径并执行 bin/flutter（或 .bat 版本）。
func runFlutter(args []string, env Env) error {
	projectRoot := findProjectRoot(env.Cwd)
	if projectRoot == "" {
		return &ExitError{
			Message: "No active Flutter SDK. Run fvmx use <repo@ref> in this project first.",
			Code:    1,
		}
	}
	sdkPath, err := resolveSDKPath(projectRoot, env.Home)
	if err != nil {
		return err
	}

	toolPath, err := flutterExecutable(sdkPath)
	if err != nil {
		return err
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" && strings.HasSuffix(strings.ToLower(toolPath), ".bat") {
		cmdArgs := append([]string{"/c", toolPath}, args...)
		cmd = exec.Command("cmd", cmdArgs...)
	} else {
		cmd = exec.Command(toolPath, args...)
	}
	cmd.Dir = env.Cwd
	cmd.Stdin = env.Stdin
	cmd.Stdout = env.Stdout
	cmd.Stderr = env.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ExitError{Code: exitErr.ExitCode()}
		}
		return err
	}
	return nil
}

// flutterExecutable 在 SDK 路径下查找 flutter 可执行文件。
// Windows 优先使用 .bat 文件以支持 Flutter 的 Windows 脚本入口。
func flutterExecutable(sdkPath string) (string, error) {
	candidates := []string{filepath.Join(sdkPath, "bin", "flutter")}
	if runtime.GOOS == "windows" {
		candidates = []string{
			filepath.Join(sdkPath, "bin", "flutter.bat"),
			filepath.Join(sdkPath, "bin", "flutter.exe"),
			filepath.Join(sdkPath, "bin", "flutter"),
		}
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", &ExitError{
		Message: "Active Flutter SDK is missing bin/flutter. Run fvmx use <repo@ref> again or reinstall the version.",
		Code:    1,
	}
}

// runDart 在项目上下文中执行 dart 命令，与 runFlutter 共享 SDK 解析逻辑
func runDart(args []string, env Env) error {
	projectRoot := findProjectRoot(env.Cwd)
	if projectRoot == "" {
		return &ExitError{
			Message: "No active Flutter SDK. Run fvmx use <repo@ref> in this project first.",
			Code:    1,
		}
	}
	sdkPath, err := resolveSDKPath(projectRoot, env.Home)
	if err != nil {
		return err
	}

	toolPath, err := dartExecutable(sdkPath)
	if err != nil {
		return err
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" && strings.HasSuffix(strings.ToLower(toolPath), ".bat") {
		cmdArgs := append([]string{"/c", toolPath}, args...)
		cmd = exec.Command("cmd", cmdArgs...)
	} else {
		cmd = exec.Command(toolPath, args...)
	}
	cmd.Dir = env.Cwd
	cmd.Stdin = env.Stdin
	cmd.Stdout = env.Stdout
	cmd.Stderr = env.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ExitError{Code: exitErr.ExitCode()}
		}
		return err
	}
	return nil
}

// dartExecutable 在 SDK 路径下查找 dart 可执行文件。
// Windows 按 .exe → .bat → 无后缀顺序查找。
func dartExecutable(sdkPath string) (string, error) {
	candidates := []string{filepath.Join(sdkPath, "bin", "dart")}
	if runtime.GOOS == "windows" {
		candidates = []string{
			filepath.Join(sdkPath, "bin", "dart.exe"),
			filepath.Join(sdkPath, "bin", "dart.bat"),
			filepath.Join(sdkPath, "bin", "dart"),
		}
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", &ExitError{
		Message: "Active Flutter SDK is missing bin/dart. Run fvmx use <repo@ref> again or reinstall the version.",
		Code:    1,
	}
}

// removeExistingLink 删除已有的 symlink 或 junction，不存在时忽略
func removeExistingLink(linkPath string) error {
	if _, err := os.Lstat(linkPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.RemoveAll(linkPath)
}

// createDirLink 创建目录链接。Unix 用 symlink，Windows 用 mklink /J 目录 junction
func createDirLink(target, linkPath string) error {
	if runtime.GOOS != "windows" {
		return os.Symlink(target, linkPath)
	}

	cmd := exec.Command("cmd", "/c", "mklink", "/J", linkPath, target)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("create junction failed: %s", detail)
	}
	return nil
}

// removeVersion 通过 git worktree remove 删除已安装版本，操作前需要用户确认
func removeVersion(home, spec string, env Env) (string, error) {
	version, err := resolveVersionOrAlias(home, spec)
	if err != nil {
		return "", err
	}

	if !confirm(env, fmt.Sprintf("Remove version %s? (y/N) ", version.ID)) {
		return "Cancelled.", nil
	}

	parsed, err := parseVersionSpec(version.ID)
	if err != nil {
		return "", err
	}

	if _, err := runGit("--git-dir", repoPath(home, parsed.Repo), "worktree", "remove", version.Path); err != nil {
		return "", err
	}

	return "Removed " + version.ID, nil
}

// aliasCommand 是 alias 子命令的调度入口
func aliasCommand(home string, args []string) (string, error) {
	if len(args) == 0 {
		return "", usageError("alias requires a subcommand.")
	}
	switch args[0] {
	case "add":
		if len(args) != 3 {
			return "", usageError("alias add requires <alias> and <repo@ref>.")
		}
		return aliasAdd(home, args[1], args[2])
	case "list":
		if len(args) != 1 {
			return "", usageError("alias list does not accept arguments.")
		}
		return aliasList(home)
	case "remove":
		if len(args) != 2 {
			return "", usageError("alias remove requires <alias>.")
		}
		return aliasRemove(home, args[1])
	default:
		return "", usageError("unknown alias command: " + args[0])
	}
}

// aliasAdd 创建别名映射到已安装版本。别名规则与 repo 名称一致。
// target 必须指向已安装版本，解析为实际版本 ID 后保存。
func aliasAdd(home, alias, target string) (string, error) {
	if !repoNamePattern.MatchString(alias) {
		return "", &ExitError{
			Message: "alias may only contain letters, numbers, dots, underscores, and hyphens",
			Code:    1,
		}
	}

	version, err := findInstalledVersion(home, target)
	if err != nil {
		return "", err
	}

	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}

	cfg.Aliases[alias] = version.ID
	if err := writeConfig(home, cfg); err != nil {
		return "", err
	}

	return fmt.Sprintf("Added alias %s -> %s", alias, version.ID), nil
}

// aliasList 按名称排序列出所有别名
func aliasList(home string) (string, error) {
	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}

	if len(cfg.Aliases) == 0 {
		return "No aliases configured.", nil
	}

	names := make([]string, 0, len(cfg.Aliases))
	for name := range cfg.Aliases {
		names = append(names, name)
	}
	sort.Strings(names)

	var builder strings.Builder
	builder.WriteString("Configured aliases:")
	for _, name := range names {
		builder.WriteString("\n  ")
		builder.WriteString(name)
		builder.WriteString("  ")
		builder.WriteString(cfg.Aliases[name])
	}
	return builder.String(), nil
}

// aliasRemove 删除别名，不涉及版本操作
func aliasRemove(home, alias string) (string, error) {
	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}

	if _, exists := cfg.Aliases[alias]; !exists {
		return "", &ExitError{Message: "alias does not exist: " + alias, Code: 1}
	}

	delete(cfg.Aliases, alias)
	if err := writeConfig(home, cfg); err != nil {
		return "", err
	}

	return "Removed alias: " + alias, nil
}

// releaseEntry 对应 Google Storage releases JSON 中的单条发布记录
type releaseEntry struct {
	Version     string `json:"version"`      // 版本号，如 "3.41.9"
	ReleaseDate string `json:"release_date"` // 发布日期，格式 "2026-05-14"
	Channel     string `json:"channel"`      // 渠道: stable / beta / dev
}

// releasesJSON 对应 Google Storage releases JSON 的顶层结构
type releasesJSON struct {
	Releases []releaseEntry `json:"releases"`
}

// releasesCommand 列出仓库可用的发布版本。
// 官方仓库（github.com/flutter/flutter）从 Google Storage API 获取；
// 非官方仓库通过 git ls-remote 获取 tags 和 branches。
func releasesCommand(home string, args []string) (string, error) {
	if len(args) == 0 {
		return "", usageError("releases requires <repo>.")
	}
	name := args[0]
	channel := "stable"
	if len(args) >= 2 {
		channel = args[1]
	}

	cfg, err := readConfig(home)
	if err != nil {
		return "", err
	}
	url, exists := cfg.Repos[name]
	if !exists {
		return "", &ExitError{Message: "unknown repo: " + name, Code: 1}
	}

	if isOfficialFlutterRepo(url) {
		return fetchOfficialReleases(url, channel)
	}
	return listRepoGitRefs(url)
}

// isOfficialFlutterRepo 判断 URL 是否指向 flutter/flutter 官方仓库
func isOfficialFlutterRepo(url string) bool {
	return strings.Contains(url, "github.com/flutter/flutter")
}

// releasesURL 根据当前操作系统返回对应平台的 Flutter releases JSON 地址
func releasesURL() string {
	switch runtime.GOOS {
	case "windows":
		return "https://storage.googleapis.com/flutter_infra_release/releases/releases_windows.json"
	case "darwin":
		return "https://storage.googleapis.com/flutter_infra_release/releases/releases_macos.json"
	default:
		return "https://storage.googleapis.com/flutter_infra_release/releases/releases_linux.json"
	}
}

// fetchOfficialReleases 从 Google Storage 获取官方发布列表，按 channel 过滤并按日期降序排列
func fetchOfficialReleases(url, channel string) (string, error) {
	resp, err := http.Get(releasesURL())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var data releasesJSON
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}

	var filtered []releaseEntry
	for _, r := range data.Releases {
		if r.Channel == channel {
			filtered = append(filtered, r)
		}
	}

	if len(filtered) == 0 {
		return fmt.Sprintf("No releases found for channel: %s", channel), nil
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ReleaseDate > filtered[j].ReleaseDate
	})

	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "Releases for origin (%s):\n", channel)
	fmt.Fprintln(tw, "  Version\tReleased")
	for _, r := range filtered {
		t, err := time.Parse("2006-01-02", r.ReleaseDate)
		date := r.ReleaseDate
		if err == nil {
			date = t.Format("2006/01/02")
		}
		fmt.Fprintf(tw, "  %s\t%s\n", r.Version, date)
	}
	tw.Flush()
	return buf.String(), nil
}

// listRepoGitRefs 通过 git ls-remote 获取非官方仓库的 tags 和 branches 列表
func listRepoGitRefs(url string) (string, error) {
	tagsOutput, err := runGit("ls-remote", "--tags", url)
	if err != nil {
		return "", err
	}
	headsOutput, err := runGit("ls-remote", "--heads", url)
	if err != nil {
		return "", err
	}

	// 解析 tags，排除 "^{}" 这种指向同一 tag 的 dereference 记录
	tagSet := map[string]bool{}
	var tags []string
	for _, line := range strings.Split(tagsOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		ref := parts[1]
		if strings.HasSuffix(ref, "^{}") {
			continue
		}
		name := strings.TrimPrefix(ref, "refs/tags/")
		if !tagSet[name] {
			tagSet[name] = true
			tags = append(tags, name)
		}
	}
	sort.Strings(tags)

	var branches []string
	for _, line := range strings.Split(headsOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimPrefix(parts[1], "refs/heads/")
		branches = append(branches, name)
	}
	sort.Strings(branches)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Releases for %s:\n", extractRepoName(url))
	if len(tags) > 0 {
		fmt.Fprintln(&buf, "  Tags:")
		for _, t := range tags {
			fmt.Fprintf(&buf, "    %s\n", t)
		}
	}
	if len(branches) > 0 {
		fmt.Fprintln(&buf, "  Branches:")
		for _, b := range branches {
			fmt.Fprintf(&buf, "    %s\n", b)
		}
	}
	return buf.String(), nil
}

// extractRepoName 从 git URL 中提取仓库名，如 "https://github.com/flutter/flutter.git" → "flutter"
func extractRepoName(url string) string {
	url = strings.TrimSuffix(url, ".git")
	if idx := strings.LastIndex(url, "/"); idx >= 0 {
		return url[idx+1:]
	}
	return url
}
