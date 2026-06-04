package fvmx

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// updateGitHubRepo 描述 update 命令查询 release 的目标仓库。
// 当前固定为当前项目自身的 release；后续如需暴露给用户再加 flag。
const updateGitHubRepo = "zjmok/fvmx"

// updateGitHubAPIBase 是 GitHub REST API 的根地址。
const updateGitHubAPIBase = "https://api.github.com"

// updateAPITimeout 限定元数据 API（releases/latest、releases/tags/...）请求的超时时间。
const updateAPITimeout = 60 * time.Second

// updateChecksumTimeout 限定 release 自带 checksums.txt 下载的超时时间。
const updateChecksumTimeout = 60 * time.Second

// updateDownloadTimeout 限定 release asset（archive）下载的超时时间。
const updateDownloadTimeout = 180 * time.Second

// updateProxyHint 在遇到网络层失败时追加到错误信息末尾，提示用户检查代理配置。
const updateProxyHint = `hint: if you are behind a proxy, set HTTP_PROXY and HTTPS_PROXY, e.g.:
  HTTP_PROXY=http://127.0.0.1:7890
  HTTPS_PROXY=http://127.0.0.1:7890
  NO_PROXY=localhost,127.0.0.1`

// isNetworkError 判断 err 是否为网络层失败（DNS、连接、超时、TLS 等），
// 用于在错误信息中追加代理提示。
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	return false
}

// withProxyHint 若 err 是网络层错误，则在消息后追加代理提示。
func withProxyHint(prefix string, err error) string {
	if isNetworkError(err) {
		return prefix + ": " + err.Error() + "\n" + updateProxyHint
	}
	return prefix + ": " + err.Error()
}

// updateOptions 汇总解析后的 fvmx update 参数。
type updateOptions struct {
	Target  string // 目标版本（v 前缀可有可无），为空时取 latest
	Check   bool   // --check：仅检查可升级版本，不下载不替换
	Include bool   // --pre：把 prerelease 也视为可升级候选
	Force   bool   // --force：允许在 dev 版本上运行
}

// parseUpdateArgs 把 os.Args 切分后的 args 解析成 updateOptions。
// args 中第一个元素是子命令之后的参数：可选的版本字符串 + flags。
// 不允许出现未知的 flag，出现时返回 ExitError(2)。
func parseUpdateArgs(args []string) (updateOptions, error) {
	opts := updateOptions{}
	positional := 0
	for _, arg := range args {
		switch {
		case arg == "--check":
			opts.Check = true
		case arg == "--pre":
			opts.Include = true
		case arg == "--force":
			opts.Force = true
		case strings.HasPrefix(arg, "-"):
			return opts, &ExitError{
				Message: "unknown option for update: " + arg + "\n\n" + usage(),
				Code:    2,
			}
		default:
			positional++
			if positional == 1 {
				opts.Target = arg
			} else {
				return opts, &ExitError{
					Message: "update accepts at most one version argument.\n\n" + usage(),
					Code:    2,
				}
			}
		}
	}
	return opts, nil
}

// parsedVersion 是拆解后的 semver（含可选 prerelease）。
type parsedVersion struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string // 原样保留（如 "beta.1"），空表示正式版
	IsZero     bool   // 是否 "0.0.0"，用于 dev 识别
}

// parseVersion 解析 semver 字符串 "v1.2.3" / "1.2.3-beta.1"。
// 解析失败返回错误。
func parseVersion(input string) (parsedVersion, error) {
	s := strings.TrimSpace(input)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return parsedVersion{}, fmt.Errorf("empty version string")
	}

	var pre string
	if idx := strings.Index(s, "-"); idx >= 0 {
		pre = s[idx+1:]
		s = s[:idx]
	}

	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return parsedVersion{}, fmt.Errorf("invalid semver %q: expected major.minor.patch", input)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return parsedVersion{}, fmt.Errorf("invalid semver %q: %w", input, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return parsedVersion{}, fmt.Errorf("invalid semver %q: %w", input, err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return parsedVersion{}, fmt.Errorf("invalid semver %q: %w", input, err)
	}

	return parsedVersion{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		Prerelease: pre,
		IsZero:     major == 0 && minor == 0 && patch == 0,
	}, nil
}

// compareVersions 比较两个 parsedVersion。
//
//	返回 -1 / 0 / 1，含义同 strings.Compare。
//	正式版 > 任何 prerelease；prerelease 之间按字典序比较。
func compareVersions(a, b parsedVersion) int {
	if a.Major != b.Major {
		if a.Major < b.Major {
			return -1
		}
		return 1
	}
	if a.Minor != b.Minor {
		if a.Minor < b.Minor {
			return -1
		}
		return 1
	}
	if a.Patch != b.Patch {
		if a.Patch < b.Patch {
			return -1
		}
		return 1
	}
	switch {
	case a.Prerelease == "" && b.Prerelease == "":
		return 0
	case a.Prerelease == "":
		return 1
	case b.Prerelease == "":
		return -1
	default:
		return strings.Compare(a.Prerelease, b.Prerelease)
	}
}

// ghRelease 对应 GitHub /releases/{tag} 和 /releases/latest 的字段。
// 我们只关心下载所需的子集，因此其他字段省略。
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Name       string    `json:"name"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
	Body       string    `json:"body"`
	Assets     []ghAsset `json:"assets"`
}

// ghAsset 对应 GitHub release asset 的字段。
type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// fetchLatestRelease 调用 GitHub API 获取最新 release。
// 当 includePre 为 false 时跳过 prerelease 和 draft。
// APIBase 仅在测试时被替换为 httptest 服务器地址。
func fetchLatestRelease(ctx context.Context, httpClient *http.Client, apiBase string, includePre bool) (ghRelease, error) {
	url := apiBase + "/repos/" + updateGitHubRepo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ghRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fvmx-update")

	resp, err := httpClient.Do(req)
	if err != nil {
		return ghRelease{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ghRelease{}, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return ghRelease{}, fmt.Errorf("decode github release: %w", err)
	}

	// 理论上 /releases/latest 已经过滤 prerelease，但老版本 GitHub 在草稿状态下
	// 会把 prerelease 漏到 latest，因此二次保险。
	if !includePre && (release.Prerelease || release.Draft) {
		return ghRelease{}, fmt.Errorf("latest release is a prerelease or draft; pass --pre to include")
	}
	return release, nil
}

// fetchReleaseByTag 按 tag 拉取精确版本，tag 自动补 v 前缀。
func fetchReleaseByTag(ctx context.Context, httpClient *http.Client, apiBase, tag string) (ghRelease, error) {
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	url := fmt.Sprintf("%s/repos/%s/releases/tags/%s", apiBase, updateGitHubRepo, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ghRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fvmx-update")

	resp, err := httpClient.Do(req)
	if err != nil {
		return ghRelease{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ghRelease{}, fmt.Errorf("release %s not found", tag)
	}
	if resp.StatusCode != http.StatusOK {
		return ghRelease{}, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return ghRelease{}, fmt.Errorf("decode github release: %w", err)
	}
	return release, nil
}

// selectAsset 从 assets 中挑选出匹配当前 OS/Arch 的 archive 文件。
// 大小写不敏感，兼容旧版 goreleaser 使用的 x86_64 命名。
// 返回 archive 文件名（用于查 checksums.txt）和 asset。
func selectAsset(assets []ghAsset, goos, goarch string) (string, ghAsset, error) {
	wantOS := strings.ToLower(goos)
	wantArch := strings.ToLower(goarch)

	archAliases := map[string][]string{
		"amd64": {"amd64", "x86_64"},
		"arm64": {"arm64", "aarch64"},
	}
	aliases, ok := archAliases[wantArch]
	if !ok {
		aliases = []string{wantArch}
	}

	var match ghAsset
	var matched bool
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if !strings.HasPrefix(name, "fvmx_") {
			continue
		}
		if !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".zip") {
			continue
		}
		if !strings.Contains(name, "_"+wantOS+"_") {
			continue
		}
		matchedArch := false
		for _, alias := range aliases {
			if strings.Contains(name, "_"+alias+".") {
				matchedArch = true
				break
			}
		}
		if !matchedArch {
			continue
		}
		match = asset
		matched = true
		break
	}
	if !matched {
		return "", ghAsset{}, fmt.Errorf("no asset found for %s/%s in release", wantOS, wantArch)
	}
	return match.Name, match, nil
}

// parseChecksums 解析 goreleaser 生成的 checksums.txt。
// 每行格式： "<sha256>  <filename>"（两个空格），可能带有 "*" 前缀（sum 工具）。
// 返回 filename -> sha256 (hex) 的映射，文件名按 basename 存储。
func parseChecksums(data []byte) (map[string]string, error) {
	checksums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 去掉 sum 工具的 "*" 二进制标记
		line = strings.TrimPrefix(line, "*")
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := strings.ToLower(fields[0])
		name := filepath.Base(fields[1])
		checksums[name] = hash
	}
	if len(checksums) == 0 {
		return nil, errors.New("checksums file is empty or malformed")
	}
	return checksums, nil
}

// downloadToBuffer 读取 HTTP body 到内存，受上下文控制超时。
func downloadToBuffer(ctx context.Context, httpClient *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "fvmx-update")
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("download not found: %s", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s returned status %d", url, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// downloadAndVerify 下载 archive 与 checksums.txt，并校验 archive 的 SHA256。
func downloadAndVerify(ctx context.Context, httpClient *http.Client, release ghRelease, assetName string) ([]byte, error) {
	checksumsURL := ""
	for _, a := range release.Assets {
		if strings.EqualFold(a.Name, "checksums.txt") {
			checksumsURL = a.BrowserDownloadURL
			break
		}
	}
	if checksumsURL == "" {
		return nil, errors.New("release does not include checksums.txt")
	}

	checksumCtx, cancel := context.WithTimeout(ctx, updateChecksumTimeout)
	defer cancel()
	checksumsRaw, err := downloadToBuffer(checksumCtx, httpClient, checksumsURL)
	if err != nil {
		return nil, fmt.Errorf("download checksums.txt: %w", err)
	}
	checksums, err := parseChecksums(checksumsRaw)
	if err != nil {
		return nil, err
	}
	want, ok := checksums[assetName]
	if !ok {
		return nil, fmt.Errorf("no checksum entry for %s", assetName)
	}

	var assetURL string
	for _, a := range release.Assets {
		if a.Name == assetName {
			assetURL = a.BrowserDownloadURL
			break
		}
	}
	if assetURL == "" {
		return nil, fmt.Errorf("asset %s not found in release", assetName)
	}

	downloadCtx, cancelDownload := context.WithTimeout(ctx, updateDownloadTimeout)
	defer cancelDownload()
	archive, err := downloadToBuffer(downloadCtx, httpClient, assetURL)
	if err != nil {
		return nil, fmt.Errorf("download asset: %w", err)
	}

	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return nil, fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, got, want)
	}
	return archive, nil
}

// extractBinary 从 tar.gz 或 zip archive 中提取出唯一的可执行文件。
// 假设 goreleaser archive 内只有一个 fvmx / fvmx.exe 文件。
func extractBinary(archive []byte, archiveName string) ([]byte, error) {
	if strings.HasSuffix(archiveName, ".zip") {
		return extractFromZip(archive)
	}
	return extractFromTarGz(archive)
}

// extractFromTarGz 解压 tar.gz 并返回名为 fvmx（或 fvmx.exe）的文件内容。
func extractFromTarGz(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("read gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var found []byte
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		base := filepath.Base(header.Name)
		if base != "fvmx" && base != "fvmx.exe" {
			continue
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read tar entry %s: %w", header.Name, err)
		}
		if found != nil {
			return nil, fmt.Errorf("archive contains multiple fvmx binaries")
		}
		found = content
	}
	if found == nil {
		return nil, errors.New("archive does not contain fvmx binary")
	}
	return found, nil
}

// extractFromZip 解压 zip 并返回名为 fvmx / fvmx.exe 的文件内容。
func extractFromZip(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("read zip: %w", err)
	}
	var found []byte
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base != "fvmx" && base != "fvmx.exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read zip entry %s: %w", f.Name, err)
		}
		if found != nil {
			return nil, errors.New("archive contains multiple fvmx binaries")
		}
		found = content
	}
	if found == nil {
		return nil, errors.New("archive does not contain fvmx binary")
	}
	return found, nil
}

// replaceExecutable 把 newContent 写到 selfPath 指向的可执行文件。
// Unix 平台直接覆盖；Windows 写 .new 后用 cmd 异步替换。
func replaceExecutable(selfPath string, newContent []byte) error {
	if runtime.GOOS == "windows" {
		return replaceExecutableWindows(selfPath, newContent)
	}
	return replaceExecutableUnix(selfPath, newContent)
}

func replaceExecutableUnix(selfPath string, newContent []byte) error {
	mode, err := currentExecMode(selfPath)
	if err != nil {
		return err
	}
	tmp := selfPath + ".new"
	if err := os.WriteFile(tmp, newContent, mode); err != nil {
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := os.Rename(tmp, selfPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}

func replaceExecutableWindows(selfPath string, newContent []byte) error {
	tmp := selfPath + ".new"
	if err := os.WriteFile(tmp, newContent, 0o644); err != nil {
		return fmt.Errorf("write new binary: %w", err)
	}
	// 使用 cmd /c 延迟执行替换，确保父进程退出后 windows 释放文件句柄。
	// ping 用来等待 ~2 秒（每次 ping 间隔 1 秒，发两次）。
	escapedTmp := strings.ReplaceAll(tmp, `"`, `\"`)
	escapedSelf := strings.ReplaceAll(selfPath, `"`, `\"`)
	script := fmt.Sprintf(
		"ping 127.0.0.1 -n 3 >nul & move /Y %s %s",
		escapedTmp, escapedSelf,
	)
	cmd := exec.Command("cmd", "/c", script)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("schedule replace: %w", err)
	}
	// 不等待 cmd 完成；让它在后台继续运行。
	go func() { _ = cmd.Wait() }()
	return nil
}

func currentExecMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat current binary: %w", err)
	}
	mode := info.Mode().Perm()
	// 至少保留可执行位
	if mode&0o111 == 0 {
		mode |= 0o755
	}
	return mode, nil
}

// runUpdate 是 fvmx update 的实现入口。
func runUpdate(args []string, env Env) (string, error) {
	opts, err := parseUpdateArgs(args)
	if err != nil {
		return "", err
	}

	// dev 构建的保护
	if env.Version == "dev" && !opts.Force {
		return "", &ExitError{
			Message: "refusing to update a dev build; pass --force to override.",
			Code:    2,
		}
	}

	apiBase := os.Getenv("FVMX_UPDATE_API")
	if apiBase == "" {
		apiBase = updateGitHubAPIBase
	}

	apiCtx, cancelAPI := context.WithTimeout(context.Background(), updateAPITimeout)
	defer cancelAPI()

	var release ghRelease
	if opts.Target != "" {
		release, err = fetchReleaseByTag(apiCtx, env.HTTPClient, apiBase, opts.Target)
	} else {
		release, err = fetchLatestRelease(apiCtx, env.HTTPClient, apiBase, opts.Include)
	}
	if err != nil {
		return "", &ExitError{Message: withProxyHint("fetch release", err), Code: 1}
	}

	currentVer, err := parseVersion(env.Version)
	if err != nil && env.Version != "dev" {
		return "", &ExitError{Message: fmt.Sprintf("invalid current version %q: %v", env.Version, err), Code: 1}
	}
	targetVer, err := parseVersion(release.TagName)
	if err != nil {
		return "", &ExitError{Message: fmt.Sprintf("invalid target tag %q: %v", release.TagName, err), Code: 1}
	}

	// --check 仅打印目标版本，不下载不替换
	if opts.Check {
		return fmt.Sprintf("Current: %s, Latest: %s", formatVersion(currentVer, env.Version), release.TagName), nil
	}

	// dev 构建下不进行 "已是最新" 比较
	if env.Version != "dev" && compareVersions(currentVer, targetVer) >= 0 {
		return fmt.Sprintf("Already on %s (latest %s).", formatVersion(currentVer, env.Version), release.TagName), nil
	}

	if !confirm(env, fmt.Sprintf("Update fvmx from %s to %s? (y/N) ", formatVersion(currentVer, env.Version), release.TagName)) {
		return "Update cancelled.", nil
	}

	assetName, asset, err := selectAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", &ExitError{Message: err.Error(), Code: 1}
	}
	_ = asset

	downloadCtx, cancelDownload := context.WithTimeout(context.Background(), updateDownloadTimeout)
	defer cancelDownload()
	archive, err := downloadAndVerify(downloadCtx, env.HTTPClient, release, assetName)
	if err != nil {
		return "", &ExitError{Message: withProxyHint("download release", err), Code: 1}
	}

	binary, err := extractBinary(archive, assetName)
	if err != nil {
		return "", &ExitError{Message: fmt.Sprintf("extract %s: %v", assetName, err), Code: 1}
	}

	selfPath := env.SelfPath
	if selfPath == "" {
		var err error
		selfPath, err = os.Executable()
		if err != nil {
			return "", &ExitError{Message: fmt.Sprintf("locate current binary: %v", err), Code: 1}
		}
	}
	if err := replaceExecutable(selfPath, binary); err != nil {
		return "", &ExitError{Message: fmt.Sprintf("replace binary: %v", err), Code: 1}
	}

	logStep(env, fmt.Sprintf("Updated fvmx to %s.", release.TagName))
	if strings.TrimSpace(release.Body) != "" {
		logStep(env, "")
		logStep(env, fmt.Sprintf("Release notes for %s:", release.TagName))
		logStep(env, strings.TrimSpace(release.Body))
	}
	return "", nil
}

// formatVersion 把 parsedVersion 还原为展示字符串，envVersion 为原始字面量（保留 v 前缀）。
func formatVersion(v parsedVersion, envVersion string) string {
	if envVersion == "dev" {
		return "dev"
	}
	if v.Prerelease != "" {
		return fmt.Sprintf("v%d.%d.%d-%s", v.Major, v.Minor, v.Patch, v.Prerelease)
	}
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}
