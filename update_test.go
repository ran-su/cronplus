package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelectReleaseAsset(t *testing.T) {
	assets := []githubReleaseAsset{
		{Name: "cronplus_linux_arm64.tar.gz"},
		{Name: "cronplus_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.test/darwin"},
	}
	got, err := selectReleaseAsset(assets, "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got.BrowserDownloadURL != "https://example.test/darwin" {
		t.Fatalf("selected asset = %+v", got)
	}
}

func TestSelectReleaseAssetFallback(t *testing.T) {
	assets := []githubReleaseAsset{
		{Name: "cronplus_v1.2.3_linux_amd64.tgz", BrowserDownloadURL: "https://example.test/linux"},
	}
	got, err := selectReleaseAsset(assets, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got.BrowserDownloadURL != "https://example.test/linux" {
		t.Fatalf("selected asset = %+v", got)
	}
}

func TestExtractCronPlusBinaryRejectsUnsafePath(t *testing.T) {
	archivePath := writeUpdateArchive(t, []updateArchiveEntry{
		{Name: "../cronplus", Body: []byte{0x7f, 'E', 'L', 'F'}},
	})
	_, cleanup, err := extractCronPlusBinary(archivePath)
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("extractCronPlusBinary() error = %v, want unsafe path error", err)
	}
}

func TestExtractCronPlusBinaryFindsNestedBinary(t *testing.T) {
	archivePath := writeUpdateArchive(t, []updateArchiveEntry{
		{Name: "README.md", Body: []byte("release notes")},
		{Name: "cronplus_darwin_arm64/cronplus", Body: []byte{0x7f, 'E', 'L', 'F'}},
	})
	binaryPath, cleanup, err := extractCronPlusBinary(archivePath)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte{0x7f, 'E', 'L', 'F'}) {
		t.Fatalf("extracted binary = %q", data)
	}
	if err := validateExecutableBinary(binaryPath); err != nil {
		t.Fatalf("validateExecutableBinary() error = %v", err)
	}
}

func TestInstallUpdatedBinaryReplacesLauncherWrapper(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source-cronplus")
	target := filepath.Join(dir, "cronplus")
	if err := os.WriteFile(source, []byte{0x7f, 'E', 'L', 'F'}, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexec /tmp/cronplus \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := installUpdatedBinary(source, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte{0x7f, 'E', 'L', 'F'}) {
		t.Fatalf("installed binary = %q", data)
	}
}

func TestRunUpdateDownloadsAndInstallsLatestRelease(t *testing.T) {
	archiveBytes := readArchiveBytes(t, writeUpdateArchive(t, []updateArchiveEntry{
		{Name: "cronplus", Body: []byte{0x7f, 'E', 'L', 'F'}},
	}))
	assetName := "cronplus_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/repos/ran-su/cronplus/releases/latest":
			return jsonResponse(http.StatusOK, githubRelease{
				TagName: "v9.9.9",
				Assets: []githubReleaseAsset{{
					Name:               assetName,
					BrowserDownloadURL: "https://downloads.example.test/asset.tar.gz",
					Size:               int64(len(archiveBytes)),
				}},
			})
		case "/asset.tar.gz":
			return bytesResponse(http.StatusOK, archiveBytes), nil
		default:
			return bytesResponse(http.StatusNotFound, []byte("not found")), nil
		}
	})}

	target := filepath.Join(t.TempDir(), "cronplus")
	var out bytes.Buffer
	if err := runUpdate(context.Background(), updateOptions{
		APIBase:    "https://api.example.test",
		TargetPath: target,
		Current:    "dev",
		HTTPClient: client,
		Stdout:     &out,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte{0x7f, 'E', 'L', 'F'}) {
		t.Fatalf("updated binary = %q", data)
	}
	if !strings.Contains(out.String(), "Installed CronPlus v9.9.9") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunUpdateRejectsLauncherScriptAsset(t *testing.T) {
	archiveBytes := readArchiveBytes(t, writeUpdateArchive(t, []updateArchiveEntry{
		{Name: "cronplus", Body: []byte("#!/bin/sh\nexit 0\n")},
	}))
	assetName := "cronplus_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/repos/ran-su/cronplus/releases/latest":
			return jsonResponse(http.StatusOK, githubRelease{
				TagName: "v9.9.9",
				Assets: []githubReleaseAsset{{
					Name:               assetName,
					BrowserDownloadURL: "https://downloads.example.test/asset.tar.gz",
				}},
			})
		case "/asset.tar.gz":
			return bytesResponse(http.StatusOK, archiveBytes), nil
		default:
			return bytesResponse(http.StatusNotFound, []byte("not found")), nil
		}
	})}

	target := filepath.Join(t.TempDir(), "cronplus")
	err := runUpdate(context.Background(), updateOptions{
		APIBase:    "https://api.example.test",
		TargetPath: target,
		Current:    "dev",
		HTTPClient: client,
	})
	if err == nil || !strings.Contains(err.Error(), "launcher script") {
		t.Fatalf("runUpdate() error = %v, want launcher script error", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target exists after rejected update: %v", statErr)
	}
}

func TestSameReleaseVersion(t *testing.T) {
	tests := []struct {
		current string
		release string
		want    bool
	}{
		{"v1.2.3", "v1.2.3", true},
		{"1.2.3", "v1.2.3", true},
		{"v1.2.3-dirty", "v1.2.3", false},
		{"dev", "v1.2.3", false},
	}
	for _, tt := range tests {
		if got := sameReleaseVersion(tt.current, tt.release); got != tt.want {
			t.Fatalf("sameReleaseVersion(%q, %q) = %v, want %v", tt.current, tt.release, got, tt.want)
		}
	}
}

type updateArchiveEntry struct {
	Name string
	Body []byte
}

func writeUpdateArchive(t *testing.T, entries []updateArchiveEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{
			Name: entry.Name,
			Mode: 0o755,
			Size: int64(len(entry.Body)),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(entry.Body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func readArchiveBytes(t *testing.T, archivePath string) []byte {
	t.Helper()
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, value any) (*http.Response, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		return nil, err
	}
	return bytesResponse(status, body.Bytes()), nil
}

func bytesResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Header:        http.Header{},
	}
}
