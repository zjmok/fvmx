package fvmx

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func createSourceRepo(t *testing.T, root string) string {
	t.Helper()

	repo := filepath.Join(root, "flutter-source")
	if err := os.MkdirAll(filepath.Join(repo, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init")
	git(t, repo, "config", "user.email", "test@example.com")
	git(t, repo, "config", "user.name", "Test User")

	flutterScript := `#!/usr/bin/env sh
echo "Flutter 3.35.0 • channel unknown • unknown source"
echo "Framework • revision abc123"
echo "Tools • Dart 3.8.0 • DevTools 2.37.0"
`
	if err := os.WriteFile(filepath.Join(repo, "bin", "flutter"), []byte(flutterScript), 0o755); err != nil {
		t.Fatal(err)
	}
	flutterBatScript := `@echo off
echo Flutter 3.35.0 - channel unknown - unknown source
echo Framework - revision abc123
echo Tools - Dart 3.8.0 - DevTools 2.37.0
`
	if err := os.WriteFile(filepath.Join(repo, "bin", "flutter.bat"), []byte(flutterBatScript), 0o755); err != nil {
		t.Fatal(err)
	}
	dartScript := `#!/usr/bin/env sh
echo "fake dart $@"
`
	if err := os.WriteFile(filepath.Join(repo, "bin", "dart"), []byte(dartScript), 0o755); err != nil {
		t.Fatal(err)
	}
	dartBatScript := `@echo off
echo fake dart %*
`
	if err := os.WriteFile(filepath.Join(repo, "bin", "dart.bat"), []byte(dartBatScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "version"), []byte("3.35.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dartSdkDir := filepath.Join(repo, "bin", "cache", "dart-sdk")
	if err := os.MkdirAll(dartSdkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dartSdkDir, "version"), []byte("3.8.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	git(t, repo, "tag", "3.35")
	return repo
}

func createTaggedSourceRepo(t *testing.T, root string, tag string) string {
	t.Helper()

	repo := createSourceRepo(t, root)
	git(t, repo, "tag", "-d", "3.35")
	git(t, repo, "tag", tag)
	return repo
}

func TestPhaseOneFlow(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	sourceRepo := createSourceRepo(t, root)
	addOutput, err := Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(addOutput, "Added repo ohos") {
		t.Fatalf("unexpected add output: %s", addOutput)
	}
	if _, err := os.Stat(filepath.Join(home, "repos", "ohos.git")); err != nil {
		t.Fatal(err)
	}

	repoListOutput, err := Run([]string{"repo", "list"}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repoListOutput, "ohos") {
		t.Fatalf("repo list output should contain repo name, got %s", repoListOutput)
	}
	if !strings.Contains(repoListOutput, sourceRepo) {
		t.Fatalf("repo list output should contain repo url, got %s", repoListOutput)
	}

	nextSourceRepo := createTaggedSourceRepo(t, filepath.Join(root, "next"), "3.36")
	setOutput, err := Run([]string{"repo", "set", "ohos", nextSourceRepo}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(setOutput, "Updated repo ohos") {
		t.Fatalf("unexpected set output: %s", setOutput)
	}
	remoteURL := git(t, filepath.Join(home, "repos", "ohos.git"), "remote", "get-url", "origin")
	if remoteURL != nextSourceRepo {
		t.Fatalf("remote URL = %s, want %s", remoteURL, nextSourceRepo)
	}

	repoListOutput, err = Run([]string{"repo", "list"}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repoListOutput, nextSourceRepo) {
		t.Fatalf("repo list output should contain updated repo url, got %s", repoListOutput)
	}

	git(t, nextSourceRepo, "tag", "3.37")
	updateOutput, err := Run([]string{"repo", "update", "ohos"}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if updateOutput != "Updated repo: ohos" {
		t.Fatalf("unexpected update output: %s", updateOutput)
	}
	updatedTag := git(t, filepath.Join(home, "repos", "ohos.git"), "rev-parse", "--verify", "3.37^{commit}")
	if updatedTag == "" {
		t.Fatalf("expected tag 3.37 to be fetched")
	}

	updateOutput, err = Run([]string{"repo", "update"}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if updateOutput != "Updated repos: ohos" {
		t.Fatalf("unexpected update all output: %s", updateOutput)
	}

	installOutput, err := Run([]string{"install", "ohos", "3.36"}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	versionID := "ohos@3.36"
	versionPath := filepath.Join(home, "versions", versionID)
	if !strings.Contains(installOutput, "Installed "+versionID) {
		t.Fatalf("unexpected install output: %s", installOutput)
	}
	if _, err := os.Stat(filepath.Join(versionPath, "bin", "flutter")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(versionPath, "bin", "cache", "dart-sdk", "version")); err != nil {
		t.Fatalf("dart-sdk version file should exist in worktree: %v", err)
	}

	listOutput, err := Run([]string{"list"}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOutput, versionID) {
		t.Fatalf("list output should contain %s, got %s", versionID, listOutput)
	}

	useOutput, err := Run([]string{"use", "ohos@3.36"}, Env{Home: home, Cwd: project, Stdin: strings.NewReader("y\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(useOutput, "Using ohos@") {
		t.Fatalf("unexpected use output: %s", useOutput)
	}
	linkPath := filepath.Join(project, ".fvmx", "flutter_sdk")
	if _, err := os.Stat(filepath.Join(linkPath, "bin", "flutter")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(useOutput, versionPath) {
		t.Fatalf("use output should contain target path %s, got %s", versionPath, useOutput)
	}
	versionFile, err := os.ReadFile(filepath.Join(project, ".fvmx", "version"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(versionFile)) != versionID {
		t.Fatalf("unexpected .fvmx/version: %s", versionFile)
	}
	rcFile, err := os.ReadFile(filepath.Join(project, ".fvmxrc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rcFile), `"flutter": "3.36"`) {
		t.Fatalf("unexpected .fvmxrc: %s", rcFile)
	}
	if !strings.Contains(string(rcFile), `"repo": "ohos"`) {
		t.Fatalf("expected repo field in .fvmxrc, got %s", rcFile)
	}

	listOutput, err = Run([]string{"list"}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOutput, "Current project: "+versionID) {
		t.Fatalf("list output should show current project version, got %s", listOutput)
	}
	if !strings.Contains(listOutput, "\033[32m*\033[0m") {
		t.Fatalf("list output should have green marker, got %s", listOutput)
	}

	var flutterOutput bytes.Buffer
	if _, err := Run([]string{"flutter", "--version"}, Env{Home: home, Cwd: project, Stdout: &flutterOutput}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(flutterOutput.String(), "Flutter 3.35.0") {
		t.Fatalf("unexpected flutter output: %s", flutterOutput.String())
	}

	var dartOutput bytes.Buffer
	if _, err := Run([]string{"dart", "--version"}, Env{Home: home, Cwd: project, Stdout: &dartOutput}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dartOutput.String(), "fake dart --version") {
		t.Fatalf("unexpected dart output: %s", dartOutput.String())
	}

	removeOutput, err := Run([]string{"remove", "ohos@3.36"}, Env{Home: home, Cwd: project, Stdin: strings.NewReader("y\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(removeOutput, "Removed "+versionID) {
		t.Fatalf("unexpected remove output: %s", removeOutput)
	}
	if _, err := os.Stat(versionPath); !os.IsNotExist(err) {
		t.Fatalf("version path should be removed")
	}

	listOutput, err = Run([]string{"list"}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if listOutput != "No versions installed." {
		t.Fatalf("unexpected empty list output: %s", listOutput)
	}
}

func TestListVersionsWhenHomeDoesNotExist(t *testing.T) {
	output, err := Run([]string{"list"}, Env{Home: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if output != "No versions installed." {
		t.Fatalf("unexpected output: %s", output)
	}
}

func TestRepoListWhenHomeDoesNotExist(t *testing.T) {
	output, err := Run([]string{"repo", "list"}, Env{Home: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if output != "No repos configured." {
		t.Fatalf("unexpected output: %s", output)
	}
}

func TestRepoSetRequiresExistingRepo(t *testing.T) {
	_, err := Run([]string{"repo", "set", "missing", "file:///tmp/flutter"}, Env{Home: filepath.Join(t.TempDir(), "home")})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "repo does not exist: missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepoUpdateWhenHomeDoesNotExist(t *testing.T) {
	output, err := Run([]string{"repo", "update"}, Env{Home: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if output != "No repos configured." {
		t.Fatalf("unexpected output: %s", output)
	}
}

func TestRepoUpdateRequiresExistingRepo(t *testing.T) {
	_, err := Run([]string{"repo", "update", "missing"}, Env{Home: filepath.Join(t.TempDir(), "home")})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "repo does not exist: missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveConfirmNo(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})
	Run([]string{"install", "ohos", "3.35"}, Env{Home: home})

	output, err := Run([]string{"remove", "ohos@3.35"}, Env{Home: home, Stdin: strings.NewReader("n\n")})
	if err != nil {
		t.Fatal(err)
	}
	if output != "Cancelled." {
		t.Fatalf("expected Cancelled, got %s", output)
	}
	if _, err := os.Stat(filepath.Join(home, "versions", "ohos@3.35")); err != nil {
		t.Fatalf("version should still exist after cancellation")
	}
}

func TestRemoveConfirmYes(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})
	Run([]string{"install", "ohos", "3.35"}, Env{Home: home})

	output, err := Run([]string{"remove", "ohos@3.35"}, Env{Home: home, Stdin: strings.NewReader("y\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Removed ohos@3.35") {
		t.Fatalf("expected removed message, got %s", output)
	}
}

func TestRepoRemoveWithVersionsFails(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})
	Run([]string{"install", "ohos", "3.35"}, Env{Home: home})

	_, err := Run([]string{"repo", "remove", "ohos"}, Env{Home: home})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "still installed") {
		t.Fatalf("expected 'still installed' error, got %v", err)
	}
}

func TestRepoRemoveSuccess(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})

	output, err := Run([]string{"repo", "remove", "ohos"}, Env{Home: home, Stdin: strings.NewReader("y\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Removed repo: ohos") {
		t.Fatalf("expected removed repo message, got %s", output)
	}
	if _, err := os.Stat(filepath.Join(home, "repos", "ohos.git")); !os.IsNotExist(err) {
		t.Fatalf("bare repo should be removed")
	}
}

func TestAliasAddListRemove(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})
	Run([]string{"install", "ohos", "3.35"}, Env{Home: home})

	addOutput, err := Run([]string{"alias", "add", "ohos_3_35", "ohos@3.35"}, Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(addOutput, "Added alias ohos_3_35") {
		t.Fatalf("unexpected alias add output: %s", addOutput)
	}

	listOutput, err := Run([]string{"alias", "list"}, Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOutput, "ohos_3_35") {
		t.Fatalf("alias list output should contain alias name, got %s", listOutput)
	}
	if !strings.Contains(listOutput, "ohos@3.35") {
		t.Fatalf("alias list output should contain target, got %s", listOutput)
	}

	rmOutput, err := Run([]string{"alias", "remove", "ohos_3_35"}, Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if rmOutput != "Removed alias: ohos_3_35" {
		t.Fatalf("unexpected alias remove output: %s", rmOutput)
	}

	listOutput, err = Run([]string{"alias", "list"}, Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if listOutput != "No aliases configured." {
		t.Fatalf("expected no aliases, got %s", listOutput)
	}
}

func TestAliasUse(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})
	Run([]string{"install", "ohos", "3.35"}, Env{Home: home})
	Run([]string{"alias", "add", "ohos_3_35", "ohos@3.35"}, Env{Home: home})

	useOutput, err := Run([]string{"use", "ohos_3_35"}, Env{Home: home, Cwd: project, Stdin: strings.NewReader("y\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(useOutput, "Using ohos@3.35") {
		t.Fatalf("unexpected use output: %s", useOutput)
	}
}

func TestListShowsAliasesColumn(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})
	Run([]string{"install", "ohos", "3.35"}, Env{Home: home})
	Run([]string{"alias", "add", "ohos_3_35", "ohos@3.35"}, Env{Home: home})

	listOutput, err := Run([]string{"list"}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOutput, "Flutter") {
		t.Fatalf("list should have Flutter column header, got: %s", listOutput)
	}
	if !strings.Contains(listOutput, "Dart") {
		t.Fatalf("list should have Dart column header, got: %s", listOutput)
	}
	if !strings.Contains(listOutput, "Aliases") {
		t.Fatalf("list should have Aliases column header, got: %s", listOutput)
	}
	if !strings.Contains(listOutput, "ohos_3_35") {
		t.Fatalf("list should show alias name in row, got: %s", listOutput)
	}
	if !strings.Contains(listOutput, "ohos@3.35") {
		t.Fatalf("list should show version ID, got: %s", listOutput)
	}
	if !strings.Contains(listOutput, "3.35.0") {
		t.Fatalf("list should show Flutter version 3.35.0, got: %s", listOutput)
	}
	if !strings.Contains(listOutput, "3.8.0") {
		t.Fatalf("list should show Dart version 3.8.0, got: %s", listOutput)
	}
}

func TestAliasRemoveViaRemove(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})
	Run([]string{"install", "ohos", "3.35"}, Env{Home: home})
	Run([]string{"alias", "add", "ohos_3_35", "ohos@3.35"}, Env{Home: home})

	output, err := Run([]string{"remove", "ohos_3_35"}, Env{Home: home, Stdin: strings.NewReader("y\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Removed ohos@3.35") {
		t.Fatalf("expected removed message, got %s", output)
	}
}

func TestAliasAddNonExistentVersion(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})

	_, err := Run([]string{"alias", "add", "myalias", "ohos@9.99"}, Env{Home: home})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "is not installed") {
		t.Fatalf("expected 'not installed' error, got %v", err)
	}
}

func TestStepLoggingOnInstall(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})

	var stdout bytes.Buffer
	result, err := Run([]string{"install", "ohos", "3.35"}, Env{Home: home, Stdout: &stdout, Stdin: strings.NewReader("y\n")})
	if err != nil {
		t.Fatal(err)
	}
	logs := stdout.String()
	if !strings.Contains(logs, "Fetching repo ohos...") {
		t.Fatalf("expected step log 'Fetching repo ohos...', got %s", logs)
	}
	if !strings.Contains(logs, "Resolving ref 3.35...") {
		t.Fatalf("expected step log 'Resolving ref 3.35...', got %s", logs)
	}
	if !strings.Contains(logs, "Creating worktree ohos@3.35...") {
		t.Fatalf("expected step log 'Creating worktree ohos@3.35...', got %s", logs)
	}
	if !strings.Contains(result, "Installed ohos@3.35") {
		t.Fatalf("expected 'Installed' in result, got %s", result)
	}
}

func TestRepoRemoveNonexistent(t *testing.T) {
	_, err := Run([]string{"repo", "remove", "missing"}, Env{Home: filepath.Join(t.TempDir(), "home")})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "repo does not exist: missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFlutterVersionOutput(t *testing.T) {
	output := `Flutter 3.7.12-ohos-1.1.7 • channel unknown • unknown source
Framework • revision abc123
Tools • Dart 2.19.6 • DevTools 2.20.0`
	fv, dv := parseFlutterVersionOutput(output)
	if fv != "3.7.12-ohos-1.1.7" {
		t.Fatalf("expected flutter 3.7.12-ohos-1.1.7, got %s", fv)
	}
	if dv != "2.19.6" {
		t.Fatalf("expected dart 2.19.6, got %s", dv)
	}

	output2 := `Flutter 3.41.9 • channel stable • https://github.com/flutter/flutter.git
Framework • revision xyz789
Tools • Dart 3.11.5 • DevTools 2.41.0`
	fv2, dv2 := parseFlutterVersionOutput(output2)
	if fv2 != "3.41.9" {
		t.Fatalf("expected flutter 3.41.9, got %s", fv2)
	}
	if dv2 != "3.11.5" {
		t.Fatalf("expected dart 3.11.5, got %s", dv2)
	}

	output3 := `Flutter 3.35.0 - channel unknown
Tools - Dart 3.8.0 - DevTools 2.37.0`
	fv3, dv3 := parseFlutterVersionOutput(output3)
	if fv3 != "3.35.0" {
		t.Fatalf("expected flutter 3.35.0, got %s", fv3)
	}
	if dv3 != "3.8.0" {
		t.Fatalf("expected dart 3.8.0, got %s", dv3)
	}
}

func TestRepoInit(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sourceRepo := createSourceRepo(t, root)

	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})

	// Select ohos (2) — already added, should call repoSet
	output, err := Run([]string{"repo", "init"}, Env{Home: home, Stdin: strings.NewReader("2\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Updated repo ohos") {
		t.Fatalf("expected 'Updated repo ohos' (repoSet path), got %s", output)
	}
	cfg, err := readConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repos["ohos"] != "https://gitcode.com/CPF-Flutter/flutter_flutter.git" {
		t.Fatalf("expected ohos URL updated to preset, got %s", cfg.Repos["ohos"])
	}
}

func TestReleasesOnNonOfficialRepo(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "testrepo", sourceRepo}, Env{Home: home})

	output, err := Run([]string{"releases", "testrepo"}, Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Tags:") {
		t.Fatalf("expected Tags: in releases output, got %s", output)
	}
	if !strings.Contains(output, "3.35") {
		t.Fatalf("expected tag 3.35 in releases output, got %s", output)
	}
	if !strings.Contains(output, "master") {
		t.Fatalf("expected master branch in releases output, got %s", output)
	}
}

func TestReleasesOnNonexistentRepo(t *testing.T) {
	_, err := Run([]string{"releases", "nonexistent"}, Env{Home: filepath.Join(t.TempDir(), "home")})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown repo: nonexistent") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepoInitCancelled(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	output, err := Run([]string{"repo", "init"}, Env{Home: home, Stdin: strings.NewReader("\n")})
	if err != nil {
		t.Fatal(err)
	}
	if output != "Cancelled." {
		t.Fatalf("expected Cancelled for empty input, got %s", output)
	}
}

func TestFindProjectRoot(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent", "child")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No .fvmx anywhere — should return ""
	if rootDir := findProjectRoot(parentDir); rootDir != "" {
		t.Fatalf("expected empty, got %s", rootDir)
	}

	// Create .fvmxrc in root — find from child dir
	if err := os.WriteFile(filepath.Join(root, ".fvmxrc"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rootDir := findProjectRoot(parentDir); rootDir != root {
		t.Fatalf("expected %s, got %s", root, rootDir)
	}

	// Create .fvmxrc in parent — should be found before root
	expectedParent := filepath.Join(root, "parent")
	if err := os.WriteFile(filepath.Join(expectedParent, ".fvmxrc"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rootDir := findProjectRoot(parentDir); rootDir != expectedParent {
		t.Fatalf("expected %s, got %s", expectedParent, rootDir)
	}

	// Run fvmx flutter from child dir works based on .fvmxrc
	sourceRepo := createSourceRepo(t, root)
	home := filepath.Join(root, "home")
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})
	Run([]string{"install", "ohos", "3.35"}, Env{Home: home})
	Run([]string{"use", "ohos@3.35"}, Env{Home: home, Cwd: expectedParent, Stdin: strings.NewReader("y\n")})

	var flutterOutput bytes.Buffer
	if _, err := Run([]string{"flutter", "--version"}, Env{Home: home, Cwd: parentDir, Stdout: &flutterOutput}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(flutterOutput.String(), "Flutter 3.35.0") {
		t.Fatalf("flutter command should work from child dir, got %s", flutterOutput.String())
	}

	var dartOutput bytes.Buffer
	if _, err := Run([]string{"dart", "--version"}, Env{Home: home, Cwd: parentDir, Stdout: &dartOutput}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dartOutput.String(), "fake dart --version") {
		t.Fatalf("dart command should work from child dir, got %s", dartOutput.String())
	}
}

func TestUseWithPubspecYaml(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceRepo := createSourceRepo(t, root)
	Run([]string{"repo", "add", "ohos", sourceRepo}, Env{Home: home})
	Run([]string{"install", "ohos", "3.35"}, Env{Home: home})

	// No pubspec.yaml — confirm n → Cancelled.
	output, err := Run([]string{"use", "ohos@3.35"}, Env{Home: home, Cwd: project, Stdin: strings.NewReader("n\n")})
	if err != nil {
		t.Fatal(err)
	}
	if output != "Cancelled." {
		t.Fatalf("expected Cancelled, got %s", output)
	}

	// No pubspec.yaml — confirm y → normal use
	output, err = Run([]string{"use", "ohos@3.35"}, Env{Home: home, Cwd: project, Stdin: strings.NewReader("y\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Using ohos@3.35") {
		t.Fatalf("expected use output, got %s", output)
	}

	// pubspec.yaml exists — no confirm needed
	otherProject := filepath.Join(root, "other")
	if err := os.MkdirAll(otherProject, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherProject, "pubspec.yaml"), []byte("name: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err = Run([]string{"use", "ohos@3.35"}, Env{Home: home, Cwd: otherProject})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Using ohos@3.35") {
		t.Fatalf("expected use output, got %s", output)
	}

	// Verify .gitignore was updated
	gitignoreContent, err := os.ReadFile(filepath.Join(otherProject, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gitignoreContent), ".fvmx/") {
		t.Fatalf("expected .fvmx/ in .gitignore, got %s", gitignoreContent)
	}
	if !strings.Contains(string(gitignoreContent), "FVMX Version Cache") {
		t.Fatalf("expected FVMX Version Cache comment in .gitignore, got %s", gitignoreContent)
	}

	// Second use should not duplicate .fvmx/ entry
	Run([]string{"use", "ohos@3.35"}, Env{Home: home, Cwd: otherProject})
	gitignoreContent2, err := os.ReadFile(filepath.Join(otherProject, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(gitignoreContent2), ".fvmx/")
	if count != 1 {
		t.Fatalf("expected exactly one .fvmx/ entry, got %d\n%s", count, gitignoreContent2)
	}
}

// --- fvmx update tests ---

func TestParseUpdateArgs(t *testing.T) {
	opts, err := parseUpdateArgs([]string{"v0.2.0", "--check", "--pre"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Target != "v0.2.0" || !opts.Check || !opts.Include || opts.Force {
		t.Fatalf("unexpected options: %+v", opts)
	}

	opts, err = parseUpdateArgs([]string{"0.3.0", "--force"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Target != "0.3.0" || !opts.Force || opts.Check || opts.Include {
		t.Fatalf("unexpected options: %+v", opts)
	}

	opts, err = parseUpdateArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Target != "" || opts.Check || opts.Include || opts.Force {
		t.Fatalf("expected empty options, got %+v", opts)
	}

	if _, err := parseUpdateArgs([]string{"v1", "v2"}); err == nil {
		t.Fatal("expected error for multiple positional args")
	}
	if _, err := parseUpdateArgs([]string{"--bogus"}); err == nil {
		t.Fatal("expected error for unknown flag")
	} else if exitErr, ok := err.(*ExitError); !ok || exitErr.Code != 2 {
		t.Fatalf("expected ExitError code 2, got %v", err)
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		input   string
		want    parsedVersion
		wantErr bool
	}{
		{"v1.2.3", parsedVersion{Major: 1, Minor: 2, Patch: 3}, false},
		{"1.2.3", parsedVersion{Major: 1, Minor: 2, Patch: 3}, false},
		{"v0.0.0", parsedVersion{IsZero: true}, false},
		{"0.0.0", parsedVersion{IsZero: true}, false},
		{"v1.2.3-beta.1", parsedVersion{Major: 1, Minor: 2, Patch: 3, Prerelease: "beta.1"}, false},
		{"1.2.3-rc.2", parsedVersion{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc.2"}, false},
		{"v1.2", parsedVersion{}, true},
		{"abc", parsedVersion{}, true},
		{"", parsedVersion{}, true},
	}
	for _, c := range cases {
		got, err := parseVersion(c.input)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseVersion(%q) expected error", c.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseVersion(%q) unexpected error: %v", c.input, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseVersion(%q) = %+v, want %+v", c.input, got, c.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	mustParse := func(s string) parsedVersion {
		v, err := parseVersion(s)
		if err != nil {
			t.Fatalf("parseVersion(%q): %v", s, err)
		}
		return v
	}
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.2.3", "2.0.0", -1},
		{"0.9.0", "1.0.0", -1},
		{"1.0.0-beta.1", "1.0.0", -1},
		{"1.0.0", "1.0.0-beta.1", 1},
		{"1.0.0-beta.1", "1.0.0-beta.2", -1},
		{"1.0.0-rc.1", "1.0.0-beta.1", 1},
	}
	for _, c := range cases {
		got := compareVersions(mustParse(c.a), mustParse(c.b))
		if got != c.want {
			t.Errorf("compareVersions(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSelectAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "fvmx_0.1.0_linux_amd64.tar.gz", BrowserDownloadURL: "u1"},
		{Name: "fvmx_0.1.0_linux_arm64.tar.gz", BrowserDownloadURL: "u2"},
		{Name: "fvmx_0.1.0_darwin_amd64.tar.gz", BrowserDownloadURL: "u3"},
		{Name: "fvmx_0.1.0_darwin_arm64.tar.gz", BrowserDownloadURL: "u4"},
		{Name: "fvmx_0.1.0_windows_amd64.zip", BrowserDownloadURL: "u5"},
		{Name: "fvmx_0.1.0_windows_arm64.zip", BrowserDownloadURL: "u6"},
		{Name: "checksums.txt", BrowserDownloadURL: "u7"},
		{Name: "LICENSE", BrowserDownloadURL: "u8"},
	}
	cases := []struct {
		os, arch string
		want     string
	}{
		{"linux", "amd64", "u1"},
		{"linux", "arm64", "u2"},
		{"darwin", "amd64", "u3"},
		{"darwin", "arm64", "u4"},
		{"windows", "amd64", "u5"},
		{"windows", "arm64", "u6"},
		{"LINUX", "AMD64", "u1"},
	}
	for _, c := range cases {
		name, asset, err := selectAsset(assets, c.os, c.arch)
		if err != nil {
			t.Errorf("selectAsset(%s, %s) error: %v", c.os, c.arch, err)
			continue
		}
		if asset.BrowserDownloadURL != c.want {
			t.Errorf("selectAsset(%s, %s) = %s, want %s", c.os, c.arch, asset.BrowserDownloadURL, c.want)
		}
		if name == "" {
			t.Errorf("selectAsset(%s, %s) returned empty name", c.os, c.arch)
		}
	}
	if _, _, err := selectAsset(assets, "freebsd", "amd64"); err == nil {
		t.Error("expected error for unsupported os")
	}
}

func TestSelectAssetCompatibleNames(t *testing.T) {
	assets := []ghAsset{
		{Name: "fvmx_0.1.0_linux_x86_64.tar.gz", BrowserDownloadURL: "u1"},
		{Name: "fvmx_0.1.0_linux_aarch64.tar.gz", BrowserDownloadURL: "u2"},
	}
	if _, asset, err := selectAsset(assets, "linux", "amd64"); err != nil {
		t.Fatalf("expected x86_64 alias to match amd64, got %v", err)
	} else if asset.BrowserDownloadURL != "u1" {
		t.Errorf("expected u1, got %s", asset.BrowserDownloadURL)
	}
	if _, asset, err := selectAsset(assets, "linux", "arm64"); err != nil {
		t.Fatalf("expected aarch64 alias to match arm64, got %v", err)
	} else if asset.BrowserDownloadURL != "u2" {
		t.Errorf("expected u2, got %s", asset.BrowserDownloadURL)
	}
}

func TestParseChecksums(t *testing.T) {
	data := []byte(`abc123def456  fvmx_0.1.0_linux_amd64.tar.gz
fed987cba012  fvmx_0.1.0_windows_amd64.zip
*111222333444  fvmx_0.1.0_darwin_amd64.tar.gz
`)
	checksums, err := parseChecksums(data)
	if err != nil {
		t.Fatal(err)
	}
	if checksums["fvmx_0.1.0_linux_amd64.tar.gz"] != "abc123def456" {
		t.Errorf("linux checksum mismatch: %v", checksums)
	}
	if checksums["fvmx_0.1.0_windows_amd64.zip"] != "fed987cba012" {
		t.Errorf("windows checksum mismatch: %v", checksums)
	}
	if checksums["fvmx_0.1.0_darwin_amd64.tar.gz"] != "111222333444" {
		t.Errorf("darwin checksum should strip '*' prefix: %v", checksums)
	}
	if _, err := parseChecksums([]byte("")); err == nil {
		t.Error("expected error for empty checksums")
	}
}

func TestExtractBinaryTarGz(t *testing.T) {
	archive := buildTarGzArchive(t, "fvmx", []byte("fake-binary-content"))
	binary, err := extractBinary(archive, "fvmx_0.1.0_linux_amd64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != "fake-binary-content" {
		t.Errorf("unexpected binary content: %q", binary)
	}
}

func TestExtractBinaryZip(t *testing.T) {
	archive := buildZipArchive(t, map[string][]byte{"fvmx.exe": []byte("windows-binary")})
	binary, err := extractBinary(archive, "fvmx_0.1.0_windows_amd64.zip")
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != "windows-binary" {
		t.Errorf("unexpected binary content: %q", binary)
	}
}

func TestExtractBinaryMissing(t *testing.T) {
	archive := buildTarGzArchive(t, "LICENSE", []byte("license text"))
	if _, err := extractBinary(archive, "fvmx_0.1.0_linux_amd64.tar.gz"); err == nil {
		t.Error("expected error when archive has no fvmx binary")
	}
}

func TestRunUpdateRefusesDev(t *testing.T) {
	env := Env{Version: "dev", HTTPClient: &http.Client{}}
	output, err := Run([]string{"update"}, env)
	if err == nil {
		t.Fatal("expected error for dev without --force")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.Code != 2 {
		t.Fatalf("expected ExitError code 2, got %v", err)
	}
	if !strings.Contains(exitErr.Message, "--force") {
		t.Errorf("error message should mention --force, got: %s", exitErr.Message)
	}
	if output != "" {
		t.Errorf("expected empty output, got %q", output)
	}
}

func TestRunUpdateForceDev(t *testing.T) {
	server, binary, _ := setupUpdateServer(t, "0.5.0", "updated-binary-data")

	binPath := filepath.Join(t.TempDir(), "fvmx"+execSuffix())
	if err := os.WriteFile(binPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	env := Env{
		Version:   "dev",
		HTTPClient: server.Client(),
		Stdout:    &stdout,
		Stdin:     strings.NewReader("y\n"),
		SelfPath:  binPath,
	}
	t.Setenv("FVMX_UPDATE_API", server.URL)

	if _, err := Run([]string{"update", "--force"}, env); err != nil {
		t.Fatalf("update --force failed: %v", err)
	}

	got := waitForReplace(t, binPath, binary)
	if !bytes.Equal(got, binary) {
		t.Errorf("binary not replaced: got %q..., want %q...", got[:min(20, len(got))], binary[:min(20, len(binary))])
	}
	if !strings.Contains(stdout.String(), "Updated fvmx to v0.5.0") {
		t.Errorf("missing updated line in output: %s", stdout.String())
	}
}

func TestRunUpdateLatest(t *testing.T) {
	server, binary, _ := setupUpdateServer(t, "1.2.3", "v1-binary-content")

	binPath := filepath.Join(t.TempDir(), "fvmx"+execSuffix())
	if err := os.WriteFile(binPath, []byte("v0-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	env := Env{
		Version:   "0.1.0",
		HTTPClient: server.Client(),
		Stdout:    &stdout,
		Stdin:     strings.NewReader("y\n"),
		SelfPath:  binPath,
	}
	t.Setenv("FVMX_UPDATE_API", server.URL)

	if _, err := Run([]string{"update"}, env); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	got := waitForReplace(t, binPath, binary)
	if !bytes.Equal(got, binary) {
		t.Errorf("binary not replaced: got %q..., want %q...", got[:min(20, len(got))], binary[:min(20, len(binary))])
	}
	if !strings.Contains(stdout.String(), "v1.2.3") {
		t.Errorf("missing version in output: %s", stdout.String())
	}
}

func TestRunUpdateCheck(t *testing.T) {
	server, _, _ := setupUpdateServer(t, "1.2.3", "v1-binary-content")

	var stdout bytes.Buffer
	env := Env{
		Version:    "0.1.0",
		HTTPClient:  server.Client(),
		Stdout:     &stdout,
		SelfPath:   filepath.Join(t.TempDir(), "fvmx"+execSuffix()),
	}
	t.Setenv("FVMX_UPDATE_API", server.URL)

	output, err := Run([]string{"update", "--check"}, env)
	if err != nil {
		t.Fatalf("update --check failed: %v", err)
	}
	if !strings.Contains(output, "Latest: v1.2.3") {
		t.Errorf("expected 'Latest: v1.2.3' in output, got: %q", output)
	}
	if !strings.Contains(output, "Current: v0.1.0") {
		t.Errorf("expected 'Current: v0.1.0' in output, got: %q", output)
	}
}

func TestRunUpdateAlreadyOnLatest(t *testing.T) {
	server, _, _ := setupUpdateServer(t, "1.2.3", "v1-binary-content")

	binPath := filepath.Join(t.TempDir(), "fvmx"+execSuffix())
	os.WriteFile(binPath, []byte("current"), 0o755)

	var stdout bytes.Buffer
	env := Env{
		Version:    "1.2.3",
		HTTPClient:  server.Client(),
		Stdout:     &stdout,
		SelfPath:   binPath,
	}
	t.Setenv("FVMX_UPDATE_API", server.URL)

	output, err := Run([]string{"update"}, env)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !strings.Contains(output, "Already on v1.2.3") {
		t.Errorf("expected 'Already on' message, got: %q", output)
	}
	got, _ := os.ReadFile(binPath)
	if string(got) != "current" {
		t.Errorf("binary should not be replaced, got: %q", got)
	}
}

func TestRunUpdateCancel(t *testing.T) {
	server, _, _ := setupUpdateServer(t, "1.2.3", "v1-binary-content")

	binPath := filepath.Join(t.TempDir(), "fvmx"+execSuffix())
	os.WriteFile(binPath, []byte("current"), 0o755)

	env := Env{
		Version:    "0.1.0",
		HTTPClient:  server.Client(),
		Stdin:      strings.NewReader("n\n"),
		SelfPath:   binPath,
	}
	t.Setenv("FVMX_UPDATE_API", server.URL)

	output, err := Run([]string{"update"}, env)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if output != "Update cancelled." {
		t.Errorf("expected cancel message, got: %q", output)
	}
	got, _ := os.ReadFile(binPath)
	if string(got) != "current" {
		t.Errorf("binary should not change on cancel, got: %q", got)
	}
}

func TestRunUpdateChecksumMismatch(t *testing.T) {
	assetName := fmt.Sprintf("fvmx_1.2.3_%s_%s.%s", runtime.GOOS, runtime.GOARCH, archiveExt())
	sum := sha256.Sum256([]byte("totally-wrong"))
	wrong := hex.EncodeToString(sum[:])

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/zjmok/fvmx/releases/latest":
			release := buildReleaseJSON("v1.2.3", assetName, wrong, serverURL+"/downloads/"+assetName, serverURL+"/downloads/checksums.txt", "Bug fixes")
			w.Header().Set("Content-Type", "application/json")
			w.Write(release)
		case "/downloads/" + assetName:
			w.Write(buildArchive([]byte("v1-binary-content")))
		case "/downloads/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", wrong, assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	serverURL = server.URL
	defer server.Close()

	binPath := filepath.Join(t.TempDir(), "fvmx"+execSuffix())
	os.WriteFile(binPath, []byte("current"), 0o755)

	env := Env{
		Version:    "0.1.0",
		HTTPClient:  server.Client(),
		Stdin:      strings.NewReader("y\n"),
		SelfPath:   binPath,
	}
	t.Setenv("FVMX_UPDATE_API", server.URL)

	_, err := Run([]string{"update"}, env)
	if err == nil {
		t.Fatal("expected error on checksum mismatch")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.Code != 1 {
		t.Fatalf("expected ExitError code 1, got %v", err)
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error should mention checksum mismatch: %v", err)
	}
	got, _ := os.ReadFile(binPath)
	if string(got) != "current" {
		t.Errorf("binary must not be replaced on checksum failure: %q", got)
	}
}

func TestRunUpdateVersionFlag(t *testing.T) {
	server, binary, _ := setupUpdateServerWithTag(t, "v0.7.0", "0.7.0", "v07-binary-content")

	binPath := filepath.Join(t.TempDir(), "fvmx"+execSuffix())
	os.WriteFile(binPath, []byte("current"), 0o755)

	env := Env{
		Version:   "0.1.0",
		HTTPClient: server.Client(),
		Stdin:     strings.NewReader("y\n"),
		SelfPath:  binPath,
	}
	t.Setenv("FVMX_UPDATE_API", server.URL)

	if _, err := Run([]string{"update", "0.7.0"}, env); err != nil {
		t.Fatalf("update 0.7.0 failed: %v", err)
	}
	got := waitForReplace(t, binPath, binary)
	if !bytes.Equal(got, binary) {
		t.Errorf("binary not replaced: got %q..., want %q...", got[:min(20, len(got))], binary[:min(20, len(binary))])
	}
}

func TestRunUpdateNetworkError(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "fvmx"+execSuffix())
	os.WriteFile(binPath, []byte("current"), 0o755)

	// Point FVMX_UPDATE_API at a closed port to trigger a connection error.
	t.Setenv("FVMX_UPDATE_API", "http://127.0.0.1:1")

	env := Env{
		Version:   "0.1.0",
		HTTPClient: &http.Client{},
		Stdin:     strings.NewReader("y\n"),
		SelfPath:  binPath,
	}
	_, err := Run([]string{"update"}, env)
	if err == nil {
		t.Fatal("expected error on network failure")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.Code != 1 {
		t.Fatalf("expected ExitError code 1, got %v", err)
	}
	if !strings.Contains(exitErr.Message, "HTTP_PROXY") {
		t.Errorf("expected proxy hint on network error, got: %q", exitErr.Message)
	}
}

func TestRunUpdateNoProxyHintOnNonNetworkError(t *testing.T) {
	// A custom server that returns 404 for the release API; that's not a network error,
	// so the proxy hint must NOT appear.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	binPath := filepath.Join(t.TempDir(), "fvmx"+execSuffix())
	os.WriteFile(binPath, []byte("current"), 0o755)

	t.Setenv("FVMX_UPDATE_API", server.URL)
	env := Env{
		Version:   "0.1.0",
		HTTPClient: server.Client(),
		Stdin:     strings.NewReader("y\n"),
		SelfPath:  binPath,
	}
	_, err := Run([]string{"update"}, env)
	if err == nil {
		t.Fatal("expected error on 404")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.Code != 1 {
		t.Fatalf("expected ExitError code 1, got %v", err)
	}
	if strings.Contains(exitErr.Message, "HTTP_PROXY") {
		t.Errorf("did not expect proxy hint on non-network error, got: %q", exitErr.Message)
	}
}

// --- test helpers ---

func execSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func archiveExt() string {
	if runtime.GOOS == "windows" {
		return "zip"
	}
	return "tar.gz"
}

// buildArchive returns the platform-appropriate archive bytes for the given binary content.
func buildArchive(content []byte) []byte {
	if runtime.GOOS == "windows" {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		hdr := &zip.FileHeader{Name: "fvmx.exe", Method: zip.Deflate}
		w, _ := zw.CreateHeader(hdr)
		w.Write(content)
		zw.Close()
		return buf.Bytes()
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "fvmx", Mode: 0o755, Size: int64(len(content))}
	tw.WriteHeader(hdr)
	tw.Write(content)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func buildTarGzArchive(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildZipArchive(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// waitForReplace polls for the binary file to match the expected content.
// On Windows, replaceExecutable is async (cmd /c ping + move), so the file is
// not updated synchronously; this helper gives it up to ~5s to settle.
func waitForReplace(t *testing.T, path string, want []byte) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := os.ReadFile(path)
		if err == nil && bytes.Equal(got, want) {
			return got
		}
		if time.Now().After(deadline) {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// setupUpdateServer 启动一个 mock GitHub 服务器，返回 (server, binary, checksum)。
// release 包含 1 个匹配的当前平台 asset + checksums.txt。
// 返回的 binary 是提取后的二进制内容（写到磁盘的内容），不是 archive 字节。
func setupUpdateServer(t *testing.T, version, binaryContent string) (*httptest.Server, []byte, string) {
	return setupUpdateServerWithTag(t, "v"+version, version, binaryContent)
}

func setupUpdateServerWithTag(t *testing.T, tag, version, binaryContent string) (*httptest.Server, []byte, string) {
	t.Helper()
	assetName := fmt.Sprintf("fvmx_%s_%s_%s.%s", version, runtime.GOOS, runtime.GOARCH, archiveExt())
	archive := buildArchive([]byte(binaryContent))
	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	assetURL := server.URL + "/downloads/" + assetName
	checksumsURL := server.URL + "/downloads/checksums.txt"

	release := buildReleaseJSON(tag, assetName, checksum, assetURL, checksumsURL, "Test release notes")

	mux.HandleFunc("/repos/zjmok/fvmx/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(release)
	})
	mux.HandleFunc("/repos/zjmok/fvmx/releases/tags/"+tag, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(release)
	})
	mux.HandleFunc("/downloads/"+assetName, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(archive)
	})
	mux.HandleFunc("/downloads/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", checksum, assetName)
	})

	return server, []byte(binaryContent), checksum
}

func buildReleaseJSON(tag, assetName, checksum, assetURL, checksumsURL, body string) []byte {
	release := ghRelease{
		TagName:    tag,
		Name:       tag,
		Prerelease: strings.Contains(tag, "-"),
		Body:       body,
		Assets: []ghAsset{
			{Name: assetName, BrowserDownloadURL: assetURL, Size: 1024},
			{Name: "checksums.txt", BrowserDownloadURL: checksumsURL, Size: int64(len(checksum))},
		},
	}
	buf, _ := json.Marshal(release)
	return buf
}

