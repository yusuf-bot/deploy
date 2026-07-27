package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"deploy/internal/config"

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

// imagesDir returns the top-level images directory under ~/.deploy/.
func imagesDir() string {
	return filepath.Join(config.DeployDirPath(), "images")
}

// appImagesDir returns the per-app images directory.
func appImagesDir(appName string) string {
	return filepath.Join(imagesDir(), appName)
}

// TarballPath returns the absolute path to a versioned tarball for an app.
func TarballPath(appName, version string) string {
	return filepath.Join(appImagesDir(appName), version+".tar")
}

// SaveImage saves a Docker image to a tarball at ~/.deploy/images/<appName>/<version>.tar.
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

	if _, err := io.Copy(f, reader); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("write tarball: %w", err)
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

// LoadImage loads a Docker image from a tarball in ~/.deploy/images/<appName>/<version>.tar.
// Returns the loaded image reference (the repo:tag that was restored).
func LoadImage(ctx context.Context, cl dockerClient, appName, version string) (string, error) {
	path := TarballPath(appName, version)
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open tarball %s: %w", path, err)
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

// RemoveTarball removes the tarball for a given app and version.
func RemoveTarball(appName, version string) error {
	path := TarballPath(appName, version)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove tarball: %w", err)
	}
	return nil
}

// ListTarballs returns sorted version strings for all tarballs of an app.
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
		if len(name) < 4 || name[len(name)-4:] != ".tar" {
			continue
		}
		versions = append(versions, name[:len(name)-4])
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
