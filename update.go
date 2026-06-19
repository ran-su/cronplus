package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultUpdateRepo       = "ran-su/cronplus"
	defaultGitHubAPIBase    = "https://api.github.com"
	maxUpdateDownloadBytes  = 200 << 20
	updateHTTPClientTimeout = 2 * time.Minute
)

type updateOptions struct {
	Repo        string
	APIBase     string
	TargetPath  string
	Current     string
	Force       bool
	DryRun      bool
	HTTPClient  *http.Client
	Stdout      io.Writer
	GitHubToken string
}

type githubRelease struct {
	TagName string               `json:"tag_name"`
	Name    string               `json:"name"`
	HTMLURL string               `json:"html_url"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func cliUpdate(args []string) int {
	flags := flag.NewFlagSet("cronplus update", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	repo := flags.String("repo", defaultUpdateRepo, "GitHub repo in owner/name form")
	targetPath := flags.String("path", "", "install path to replace; defaults to the current stable executable or platform install path")
	force := flags.Bool("force", false, "install even when the latest release matches the current version")
	dryRun := flags.Bool("dry-run", false, "print the selected release, asset, and target path without installing")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument: %s\n", flags.Arg(0))
		return 2
	}

	err := runUpdate(context.Background(), updateOptions{
		Repo:        *repo,
		TargetPath:  *targetPath,
		Current:     version,
		Force:       *force,
		DryRun:      *dryRun,
		GitHubToken: strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		Stdout:      os.Stdout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		return 1
	}
	return 0
}

func runUpdate(ctx context.Context, opts updateOptions) error {
	if opts.Repo == "" {
		opts.Repo = defaultUpdateRepo
	}
	if opts.APIBase == "" {
		opts.APIBase = defaultGitHubAPIBase
	}
	if opts.Current == "" {
		opts.Current = version
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: updateHTTPClientTimeout}
	}
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}

	targetPath, err := resolveUpdateTargetPath(opts.TargetPath)
	if err != nil {
		return err
	}
	release, err := fetchLatestGitHubRelease(ctx, opts.HTTPClient, opts.APIBase, opts.Repo, opts.GitHubToken)
	if err != nil {
		return err
	}
	asset, err := selectReleaseAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("%w for %s/%s in release %s", err, runtime.GOOS, runtime.GOARCH, release.TagName)
	}

	fmt.Fprintf(opts.Stdout, "Latest release: %s\n", release.TagName)
	fmt.Fprintf(opts.Stdout, "Asset: %s\n", asset.Name)
	fmt.Fprintf(opts.Stdout, "Install path: %s\n", targetPath)

	if sameReleaseVersion(opts.Current, release.TagName) && !opts.Force {
		fmt.Fprintf(opts.Stdout, "Already running %s. Use --force to reinstall it.\n", release.TagName)
		return nil
	}
	if opts.DryRun {
		fmt.Fprintln(opts.Stdout, "Dry run complete; no files changed.")
		return nil
	}

	archivePath, err := downloadUpdateArchive(ctx, opts.HTTPClient, asset.BrowserDownloadURL)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	binaryPath, cleanup, err := extractCronPlusBinary(archivePath)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err
	}
	if err := validateExecutableBinary(binaryPath); err != nil {
		return fmt.Errorf("downloaded binary is not usable: %w", err)
	}
	if err := installUpdatedBinary(binaryPath, targetPath); err != nil {
		return err
	}

	fmt.Fprintf(opts.Stdout, "Installed CronPlus %s to %s\n", release.TagName, targetPath)
	fmt.Fprintln(opts.Stdout, "Restart any running CronPlus daemon to use the updated binary.")
	return nil
}

func fetchLatestGitHubRelease(ctx context.Context, client *http.Client, apiBase, repo, token string) (githubRelease, error) {
	if err := validateGitHubRepo(repo); err != nil {
		return githubRelease{}, err
	}
	endpoint, err := url.JoinPath(strings.TrimRight(apiBase, "/"), "repos", repo, "releases", "latest")
	if err != nil {
		return githubRelease{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cronplus/"+version)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return githubRelease{}, fmt.Errorf("fetch latest release: GitHub returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, fmt.Errorf("decode latest release: %w", err)
	}
	if release.TagName == "" {
		return githubRelease{}, errors.New("latest release response did not include tag_name")
	}
	return release, nil
}

func validateGitHubRepo(repo string) error {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("repo must be in owner/name form: %s", repo)
	}
	for _, part := range parts {
		if part == "." || part == ".." || strings.Contains(part, "..") {
			return fmt.Errorf("repo contains an unsafe path component: %s", repo)
		}
	}
	return nil
}

func selectReleaseAsset(assets []githubReleaseAsset, goos, goarch string) (githubReleaseAsset, error) {
	exact := fmt.Sprintf("cronplus_%s_%s.tar.gz", goos, goarch)
	for _, asset := range assets {
		if strings.EqualFold(asset.Name, exact) {
			return asset, nil
		}
	}
	needle := fmt.Sprintf("_%s_%s", goos, goarch)
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if strings.HasPrefix(name, "cronplus_") && strings.Contains(name, needle) && (strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz")) {
			return asset, nil
		}
	}
	return githubReleaseAsset{}, errors.New("no matching release asset")
}

func downloadUpdateArchive(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse asset URL: %w", err)
	}
	if !allowedUpdateDownloadURL(parsed) {
		return "", fmt.Errorf("refusing non-HTTPS release asset URL: %s", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "cronplus/"+version)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download release asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("download release asset: server returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if resp.ContentLength > maxUpdateDownloadBytes {
		return "", fmt.Errorf("release asset is too large: %d bytes", resp.ContentLength)
	}

	file, err := os.CreateTemp("", "cronplus-update-*.tar.gz")
	if err != nil {
		return "", err
	}
	archivePath := file.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(archivePath)
		}
	}()

	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, maxUpdateDownloadBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		err = fmt.Errorf("write release asset: %w", copyErr)
		return "", err
	}
	if closeErr != nil {
		err = fmt.Errorf("close release asset: %w", closeErr)
		return "", err
	}
	if written > maxUpdateDownloadBytes {
		err = fmt.Errorf("release asset exceeds %d bytes", maxUpdateDownloadBytes)
		return "", err
	}
	return archivePath, nil
}

func allowedUpdateDownloadURL(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func extractCronPlusBinary(archivePath string) (string, func(), error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return "", nil, err
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return "", nil, fmt.Errorf("open release archive: %w", err)
	}
	defer gzipReader.Close()

	tempDir, err := os.MkdirTemp("", "cronplus-update-extract-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}
	binaryPath := filepath.Join(tempDir, "cronplus")
	found := false
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("read release archive: %w", err)
		}
		if !safeTarPath(header.Name) {
			cleanup()
			return "", nil, fmt.Errorf("release archive contains unsafe path: %s", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		if path.Base(header.Name) != "cronplus" {
			continue
		}
		if found {
			cleanup()
			return "", nil, errors.New("release archive contains multiple cronplus binaries")
		}
		found = true
		out, err := os.OpenFile(binaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		written, copyErr := io.Copy(out, io.LimitReader(tarReader, maxUpdateDownloadBytes+1))
		closeErr := out.Close()
		if copyErr != nil {
			cleanup()
			return "", nil, fmt.Errorf("extract cronplus binary: %w", copyErr)
		}
		if closeErr != nil {
			cleanup()
			return "", nil, fmt.Errorf("close extracted binary: %w", closeErr)
		}
		if written > maxUpdateDownloadBytes {
			cleanup()
			return "", nil, fmt.Errorf("cronplus binary exceeds %d bytes", maxUpdateDownloadBytes)
		}
	}
	if !found {
		cleanup()
		return "", nil, errors.New("release archive did not contain a cronplus binary")
	}
	return binaryPath, cleanup, nil
}

func safeTarPath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") {
		return false
	}
	clean := path.Clean(name)
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return false
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func resolveUpdateTargetPath(rawPath string) (string, error) {
	if strings.TrimSpace(rawPath) != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path, err := expandAndAbsPath(strings.TrimSpace(rawPath), home)
		if err != nil {
			return "", err
		}
		return path, nil
	}

	if executablePath, err := os.Executable(); err == nil {
		if absPath, err := filepath.Abs(executablePath); err == nil {
			if evaluatedPath, err := filepath.EvalSymlinks(absPath); err == nil {
				absPath = evaluatedPath
			}
			if !isLikelyUnstableExecutablePath(absPath) {
				if script, err := looksLikeLauncherScript(absPath); err == nil && !script {
					return absPath, nil
				}
			}
		}
	}

	return defaultPlatformInstallPath(), nil
}

func defaultPlatformInstallPath() string {
	if runtime.GOOS == "darwin" {
		if info, err := os.Stat("/opt/homebrew/bin"); err == nil && info.IsDir() {
			return "/opt/homebrew/bin/cronplus"
		}
	}
	return "/usr/local/bin/cronplus"
}

func installUpdatedBinary(sourcePath, targetPath string) error {
	if !filepath.IsAbs(targetPath) {
		return fmt.Errorf("install path must be absolute: %s", targetPath)
	}
	if info, err := os.Stat(targetPath); err == nil && info.IsDir() {
		return fmt.Errorf("install path is a directory: %s", targetPath)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect install path: %w", err)
	}
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	temp, err := os.CreateTemp(targetDir, ".cronplus-update-*")
	if err != nil {
		return fmt.Errorf("create install temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if _, err := io.Copy(temp, source); err != nil {
		_ = temp.Close()
		return fmt.Errorf("copy update binary: %w", err)
	}
	if err := temp.Chmod(0o755); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set update binary permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close update binary: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("replace install binary: %w", err)
	}
	return nil
}

func sameReleaseVersion(current, releaseTag string) bool {
	current = strings.TrimSpace(current)
	releaseTag = strings.TrimSpace(releaseTag)
	if current == "" || releaseTag == "" || current == "dev" || strings.Contains(current, "dirty") {
		return false
	}
	return normalizeVersionString(current) == normalizeVersionString(releaseTag)
}

func normalizeVersionString(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "v")
}
