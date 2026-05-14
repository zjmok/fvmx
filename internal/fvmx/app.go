package fvmx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

const configFile = "config.json"
const projectStateDir = ".fvmx"
const projectConfigFile = ".fvmxrc"

var repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type ExitError struct {
	Message string
	Code    int
}

func (e *ExitError) Error() string {
	return e.Message
}

type Env struct {
	Home   string
	Cwd    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type config struct {
	Repos map[string]string `json:"repos"`
}

type projectConfig struct {
	Flutter string `json:"flutter"`
}

type versionSpec struct {
	Repo  string
	Label string
}

type installedVersion struct {
	ID   string
	Path string
}

func Run(args []string, env Env) (string, error) {
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
		return repoCommand(env.Home, args[1:])
	case "install":
		if len(args) != 3 {
			return "", usageError("install requires <repo> and <ref>.")
		}
		return install(env.Home, args[1], args[2])
	case "list":
		if len(args) != 1 {
			return "", usageError("list does not accept arguments.")
		}
		return listVersions(env.Home, env.Cwd)
	case "use":
		if len(args) != 2 {
			return "", usageError("use requires <repo@ref>.")
		}
		return useVersion(env.Home, args[1], env.Cwd)
	case "flutter":
		if err := runFlutter(args[1:], env); err != nil {
			return "", err
		}
		return "", nil
	case "remove":
		if len(args) != 2 {
			return "", usageError("remove requires <repo@ref>.")
		}
		return removeVersion(env.Home, args[1])
	default:
		return "", usageError(fmt.Sprintf("unknown command: %s", args[0]))
	}
}

func usage() string {
	return `Usage:
  fvmx repo add <name> <url>
  fvmx repo set <name> <url>
  fvmx repo list
  fvmx repo update [name]
  fvmx install <repo> <ref>
  fvmx list
  fvmx use <repo@ref>
  fvmx flutter [args...]
  fvmx remove <repo@ref>

Environment:
  FVMX_HOME  Override the storage directory (default: ~/.fvmx)`
}

func usageError(message string) error {
	return &ExitError{Message: message + "\n\n" + usage(), Code: 2}
}

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

func ensureBaseDirs(home string) error {
	if err := os.MkdirAll(filepath.Join(home, "repos"), 0o755); err != nil {
		return err
	}
	return os.MkdirAll(versionsDir(home), 0o755)
}

func repoPath(home, name string) string {
	return filepath.Join(home, "repos", name+".git")
}

func versionsDir(home string) string {
	return filepath.Join(home, "versions")
}

func readConfig(home string) (config, error) {
	path := filepath.Join(home, configFile)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return config{Repos: map[string]string{}}, nil
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
	return cfg, nil
}

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

func assertRepoName(name string) error {
	if !repoNamePattern.MatchString(name) {
		return &ExitError{
			Message: "repo name may only contain letters, numbers, dots, underscores, and hyphens",
			Code:    1,
		}
	}
	return nil
}

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

func resolveCommit(repoPath, ref string) (string, error) {
	return runGit("--git-dir", repoPath, "rev-parse", "--verify", ref+"^{commit}")
}

func shortCommit(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}

func versionLabel(ref string, commit string) string {
	label := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(ref, "-")
	label = strings.Trim(label, "-.")
	if label == "" {
		return shortCommit(commit)
	}
	return label
}

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

func repoCommand(home string, args []string) (string, error) {
	if len(args) == 0 {
		return "", usageError("repo requires a subcommand.")
	}

	switch args[0] {
	case "add":
		if len(args) != 3 {
			return "", usageError("repo add requires <name> and <url>.")
		}
		return repoAdd(home, args[1], args[2])
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
		return repoUpdate(home, name)
	default:
		return "", usageError("unknown repo command: " + args[0])
	}
}

func repoAdd(home, name, url string) (string, error) {
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

	if _, err := runGit("clone", "--bare", url, target); err != nil {
		return "", err
	}

	cfg.Repos[name] = url
	if err := writeConfig(home, cfg); err != nil {
		return "", err
	}

	return fmt.Sprintf("Added repo %s: %s", name, url), nil
}

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

func repoUpdate(home, name string) (string, error) {
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
		if err := repoFetch(home, repoName); err != nil {
			return "", err
		}
	}

	return "Updated repos: " + strings.Join(names, ", "), nil
}

func repoFetch(home, name string) error {
	repo := repoPath(home, name)
	if _, err := os.Stat(repo); err != nil {
		return &ExitError{Message: "bare repository is missing: " + repo, Code: 1}
	}
	_, err := runGit("--git-dir", repo, "fetch", "--all", "--tags")
	return err
}

func install(home, name, ref string) (string, error) {
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

	if err := repoFetch(home, name); err != nil {
		return "", err
	}

	repo := repoPath(home, name)
	commit, err := resolveCommit(repo, ref)
	if err != nil {
		return "", err
	}

	id := name + "@" + versionLabel(ref, commit)
	target := filepath.Join(versionsDir(home), id)
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return fmt.Sprintf("Version already installed: %s\n%s", id, target), nil
	}

	if _, err := runGit("--git-dir", repo, "worktree", "add", target, commit); err != nil {
		return "", err
	}

	return fmt.Sprintf("Installed %s\n%s", id, target), nil
}

func listVersions(home, projectDir string) (string, error) {
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
	current := currentProjectVersion(projectDir, versions)
	var builder strings.Builder
	if current != "" {
		builder.WriteString("Current project: ")
		builder.WriteString(current)
		builder.WriteString("\n")
	}
	builder.WriteString("Installed versions:")
	for _, version := range versions {
		builder.WriteString("\n  ")
		if version == current {
			builder.WriteString("* ")
		} else {
			builder.WriteString("  ")
		}
		builder.WriteString(version)
		builder.WriteString("  ")
		builder.WriteString(filepath.Join(versionsDir(home), version))
	}
	return builder.String(), nil
}

func currentProjectVersion(projectDir string, installed []string) string {
	versionPath := filepath.Join(projectDir, projectStateDir, "version")
	if content, err := os.ReadFile(versionPath); err == nil {
		current := strings.TrimSpace(string(content))
		for _, version := range installed {
			if version == current {
				return version
			}
		}
	}

	rcPath := filepath.Join(projectDir, projectConfigFile)
	content, err := os.ReadFile(rcPath)
	if err != nil {
		return ""
	}

	var cfg projectConfig
	if err := json.Unmarshal(content, &cfg); err != nil || cfg.Flutter == "" {
		return ""
	}

	matches := []string{}
	for _, version := range installed {
		if strings.HasSuffix(version, "@"+cfg.Flutter) {
			matches = append(matches, version)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func useVersion(home, spec, projectDir string) (string, error) {
	version, err := findInstalledVersion(home, spec)
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

func writeProjectSelection(projectDir, versionID string) error {
	stateDir := filepath.Join(projectDir, projectStateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(stateDir, "version"), []byte(versionID+"\n"), 0o644); err != nil {
		return err
	}

	label := versionID
	if index := strings.Index(versionID, "@"); index >= 0 && index < len(versionID)-1 {
		label = versionID[index+1:]
	}
	content, err := json.MarshalIndent(projectConfig{Flutter: label}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(projectDir, projectConfigFile), append(content, '\n'), 0o644)
}

func runFlutter(args []string, env Env) error {
	sdkPath := filepath.Join(env.Cwd, projectStateDir, "flutter_sdk")
	if info, err := os.Stat(sdkPath); err != nil || !info.IsDir() {
		return &ExitError{
			Message: "No active Flutter SDK. Run fvmx use <repo@ref> in this project first.",
			Code:    1,
		}
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

func removeExistingLink(linkPath string) error {
	if _, err := os.Lstat(linkPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.RemoveAll(linkPath)
}

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

func removeVersion(home, spec string) (string, error) {
	parsed, err := parseVersionSpec(spec)
	if err != nil {
		return "", err
	}

	version, err := findInstalledVersion(home, spec)
	if err != nil {
		return "", err
	}

	if _, err := runGit("--git-dir", repoPath(home, parsed.Repo), "worktree", "remove", version.Path); err != nil {
		return "", err
	}

	return "Removed " + version.ID, nil
}
