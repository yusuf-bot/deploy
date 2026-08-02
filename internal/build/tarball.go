package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"deploy/internal/config"

	"github.com/klauspost/compress/zstd"
	moby "github.com/moby/moby/client"
)

// dockerClient defines the subset of the Docker SDK client that the build
// and tarball packages require. This allows testing with a mock.
type dockerClient interface {
	ImageBuild(ctx context.Context, buildContext io.Reader, opts moby.ImageBuildOptions) (moby.ImageBuildResult, error)
	ImageSave(ctx context.Context, images []string, opts ...moby.ImageSaveOption) (moby.ImageSaveResult, error)
	ImageLoad(ctx context.Context, input io.Reader, opts ...moby.ImageLoadOption) (moby.ImageLoadResult, error)
	ImageInspect(ctx context.Context, imageID string, opts ...moby.ImageInspectOption) (moby.ImageInspectResult, error)
}

// TarballExt is the extension for zstd-compressed image tarballs.
const TarballExt = ".tar.zst"

// LegacyTarballExt is the extension for uncompressed image tarballs written by
// older versions. These are read for backward compatibility but never written.
const LegacyTarballExt = ".tar"

// imagesDir returns the top-level images directory under ~/.deploy/.
func imagesDir() string {
	return filepath.Join(config.DeployDirPath(), "images")
}

// appImagesDir returns the per-app images directory.
func appImagesDir(appName string) string {
	return filepath.Join(imagesDir(), appName)
}

// TarballPath returns the absolute path to a versioned tarball for an app.
// New saves always use the zstd-compressed extension.
func TarballPath(appName, version string) string {
	return filepath.Join(appImagesDir(appName), version+TarballExt)
}

// OpenTarball opens the saved image for an app+version for reading,
// transparently decompressing zstd files. It checks the zstd format first and
// falls back to the legacy uncompressed .tar format for backward compatibility.
// The returned io.ReadCloser must be closed by the caller.
func OpenTarball(appName, version string) (io.ReadCloser, error) {
	dir := appImagesDir(appName)

	zstPath := filepath.Join(dir, version+TarballExt)
	if _, err := os.Stat(zstPath); err == nil {
		f, err := os.Open(zstPath)
		if err != nil {
			return nil, fmt.Errorf("open tarball %s: %w", zstPath, err)
		}
		dec, err := zstd.NewReader(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("open zstd tarball %s: %w", zstPath, err)
		}
		return &zstdReadCloser{Reader: dec, dec: dec, file: f}, nil
	}

	tarPath := filepath.Join(dir, version+LegacyTarballExt)
	f, err := os.Open(tarPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no saved image found for %s %s", appName, version)
		}
		return nil, fmt.Errorf("open tarball %s: %w", tarPath, err)
	}
	return f, nil
}

// zstdReadCloser closes both the zstd decoder and the underlying file.
type zstdReadCloser struct {
	io.Reader
	dec  *zstd.Decoder
	file *os.File
}

func (z *zstdReadCloser) Close() error {
	z.dec.Close()
	return z.file.Close()
}

// SaveImage saves a Docker image to a zstd-compressed tarball at
// ~/.deploy/images/<appName>/<version>.tar.zst.
// The write is atomic: it writes to a temp file first, then renames.
// Returns the absolute path to the saved tarball.
func SaveImage(ctx context.Context, cl dockerClient, imageRef, appName, version string) (string, error) {
	dir := appImagesDir(appName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create images dir: %w", err)
	}

	reader, err := cl.ImageSave(ctx, []string{imageRef})
	if err != nil {
		return "", fmt.Errorf("docker save: %w", err)
	}
	defer reader.Close()

	finalPath := TarballPath(appName, version)
	tmpPath := finalPath + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	zw, err := zstd.NewWriter(f)
	if err != nil {
		f.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("create zstd writer: %w", err)
	}

	if _, err := io.Copy(zw, reader); err != nil {
		zw.Close()
		f.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("write tarball: %w", err)
	}
	// Close the encoder first so all compressed data is flushed to disk.
	if err := zw.Close(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("flush zstd tarball: %w", err)
	}
	f.Close()

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("rename tarball: %w", err)
	}

	abs, err := filepath.Abs(finalPath)
	if err != nil {
		// Return the path even if abs fails
		return finalPath, nil
	}
	return abs, nil
}

// LoadImage loads a Docker image from a tarball in ~/.deploy/images/<appName>/<version>.tar.zst
// (falling back to the legacy .tar format). Returns the loaded image reference
// (the repo:tag that was restored).
func LoadImage(ctx context.Context, cl dockerClient, appName, version string) (string, error) {
	f, err := OpenTarball(appName, version)
	if err != nil {
		return "", err
	}
	defer f.Close()

	resp, err := cl.ImageLoad(ctx, f, moby.ImageLoadWithQuiet(true))
	if err != nil {
		return "", fmt.Errorf("docker load: %w", err)
	}
	defer resp.Close()

	// Drain the response body
	io.Copy(io.Discard, resp)

	return fmt.Sprintf("%s:%s", appName, version), nil
}

// RemoveTarball removes the tarball for a given app and version. Both the
// zstd and legacy formats are removed so stray files never accumulate.
func RemoveTarball(appName, version string) error {
	for _, ext := range []string{TarballExt, LegacyTarballExt} {
		path := filepath.Join(appImagesDir(appName), version+ext)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove tarball: %w", err)
		}
	}
	return nil
}

// ListTarballs returns sorted version strings for all tarballs of an app.
// Both zstd (.tar.zst) and legacy (.tar) formats are included.
// The versions are sorted lexicographically (matching semver ordering in most cases).
func ListTarballs(appName string) ([]string, error) {
	dir := appImagesDir(appName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list tarballs: %w", err)
	}

	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, TarballExt):
			versions = append(versions, name[:len(name)-len(TarballExt)])
		case strings.HasSuffix(name, LegacyTarballExt):
			versions = append(versions, name[:len(name)-len(LegacyTarballExt)])
		}
	}
	sort.Strings(versions)
	return versions, nil
}

// CleanOldTarballs removes all but the most recent N tarballs for an app.
func CleanOldTarballs(appName string, keep int) error {
	versions, err := ListTarballs(appName)
	if err != nil {
		return err
	}
	if len(versions) <= keep {
		return nil
	}
	// versions are sorted ascending; remove oldest (first ones)
	toRemove := versions[:len(versions)-keep]
	for _, v := range toRemove {
		if err := RemoveTarball(appName, v); err != nil {
			return fmt.Errorf("clean old tarball %s/%s: %w", appName, v, err)
		}
	}
	return nil
}

// RemoveAllTarballs removes all tarballs for a given app.
func RemoveAllTarballs(appName string) error {
	dir := appImagesDir(appName)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove all tarballs: %w", err)
	}
	return nil
}
