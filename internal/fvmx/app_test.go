package fvmx

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

	useOutput, err := Run([]string{"use", "ohos@3.36"}, Env{Home: home, Cwd: project})
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

	useOutput, err := Run([]string{"use", "ohos_3_35"}, Env{Home: home, Cwd: project})
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
