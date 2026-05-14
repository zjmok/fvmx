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

	if err := os.WriteFile(filepath.Join(repo, "bin", "flutter"), []byte("#!/usr/bin/env sh\necho fake flutter \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "bin", "flutter.bat"), []byte("@echo off\r\necho fake flutter %*\r\n"), 0o755); err != nil {
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
	if _, err := os.Stat(filepath.Join(versionPath, "bin", "cache")); !os.IsNotExist(err) {
		t.Fatalf("bin/cache should not be created during install")
	}

	listOutput, err := Run([]string{"list"}, Env{Home: home, Cwd: project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOutput, versionID) {
		t.Fatalf("list output should contain %s, got %s", versionID, listOutput)
	}
	if !strings.Contains(listOutput, versionPath) {
		t.Fatalf("list output should contain %s, got %s", versionPath, listOutput)
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
	if !strings.Contains(listOutput, "* "+versionID) {
		t.Fatalf("list output should mark current version, got %s", listOutput)
	}

	var flutterOutput bytes.Buffer
	if _, err := Run([]string{"flutter", "--version"}, Env{Home: home, Cwd: project, Stdout: &flutterOutput}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(flutterOutput.String(), "fake flutter --version") {
		t.Fatalf("unexpected flutter output: %s", flutterOutput.String())
	}

	removeOutput, err := Run([]string{"remove", "ohos@3.36"}, Env{Home: home, Cwd: project})
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
