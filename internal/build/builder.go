package build

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"deploy/internal/config"
	"deploy/internal/types"

	moby "github.com/moby/moby/client"
)

// Builder handles Docker image building, tagging, and tarball management.
type Builder struct {
	client dockerClient
	cfg    *types.DeployConfig
}

// NewBuilder creates a new Builder with the given Docker client.
func NewBuilder(client dockerClient) *Builder {
	return &Builder{client: client}
}

// buildContextReader creates a gzipped tar archive of the given directory
// suitable for use as a Docker build context.
func buildContextReader(contextDir string) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		err := tarDir(pw, contextDir)
		pw.CloseWithError(err)
	}()
	return pr, nil
}

// tarDir writes a gzipped tar archive of dir to w.
func tarDir(w io.WriteCloser, dir string) error {
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	err := filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get the relative path
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		header, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return fmt.Errorf("tar header for %s: %w", relPath, err)
		}
		header.Name = filepath.ToSlash(relPath)

		if fi.IsDir() {
			header.Name += "/"
		}

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write tar header for %s: %w", relPath, err)
		}

		if !fi.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("open %s: %w", path, err)
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return fmt.Errorf("copy %s: %w", path, err)
			}
		}
		return nil
	})

	if err != nil {
		tw.Close()
		gw.Close()
		return err
	}

	if err := tw.Close(); err != nil {
		gw.Close()
		return err
	}
	return gw.Close()
}

// Build performs a full build: loads config, builds the Docker image,
// saves a tarball, and cleans up old tarballs.
func (b *Builder) Build(ctx context.Context, appDir string) (*types.BuildResult, error) {
	// 1. Load deploy.yml from appDir
	cfg, err := config.LoadDeployConfig(filepath.Join(appDir, "deploy.yml"))
	if err != nil {
		return nil, fmt.Errorf("load deploy config: %w", err)
	}
	b.cfg = cfg

	// 2. Detect stack (informational)
	_ = config.DetectStack(appDir)

	// 3. Determine version
	version := VersionTag(appDir)

	// 4. Build context
	contextDir := filepath.Join(appDir, cfg.Build.Context)
	buildCtx, err := buildContextReader(contextDir)
	if err != nil {
		return nil, fmt.Errorf("create build context: %w", err)
	}
	defer buildCtx.Close()

	imageRef := fmt.Sprintf("%s:%s", cfg.App, version)

	// 5. Build args: convert map[string]string to map[string]*string
	buildArgs := make(map[string]*string)
	for k, v := range cfg.Build.Args {
		val := v
		buildArgs[k] = &val
	}

	opts := moby.ImageBuildOptions{
		Tags:       []string{imageRef},
		Dockerfile: cfg.Build.Dockerfile,
		BuildArgs:  buildArgs,
		Target:     cfg.Build.Target,
		Remove:     true,
		Context:    buildCtx,
	}

	resp, err := b.client.ImageBuild(ctx, buildCtx, opts)
	if err != nil {
		return nil, fmt.Errorf("docker build: %w", err)
	}
	defer resp.Body.Close()
	// Drain build output
	io.Copy(io.Discard, resp.Body)

	// 6. Inspect image to get digest
	imageID, err := getImageDigest(ctx, b.client, imageRef)
	if err != nil {
		return nil, fmt.Errorf("get image digest: %w", err)
	}

	// 7. Save tarball
	tarballPath, err := SaveImage(ctx, b.client, imageRef, cfg.App, version)
	if err != nil {
		return nil, fmt.Errorf("save image: %w", err)
	}

	// 8. Clean up old tarballs (keep last 5)
	if err := CleanOldTarballs(cfg.App, 5); err != nil {
		// Non-fatal — log but don't fail the build
		_ = err
	}

	return &types.BuildResult{
		Version:     version,
		ImageRef:    imageRef,
		TarballPath: tarballPath,
		ImageDigest: imageID,
	}, nil
}

// getImageDigest inspects the image and returns its ID (sha256).
func getImageDigest(ctx context.Context, cl dockerClient, imageRef string) (string, error) {
	info, err := cl.ImageInspect(ctx, imageRef)
	if err != nil {
		return "", fmt.Errorf("inspect image %s: %w", imageRef, err)
	}
	// The ID is in the form "sha256:..." — return as-is.
	if strings.HasPrefix(info.ID, "sha256:") {
		return info.ID, nil
	}
	return "sha256:" + info.ID, nil
}

// CreateBuildContext creates a gzipped tar archive of the given directory
// suitable for use as a Docker build context.
func CreateBuildContext(contextDir string, _ string) (io.ReadCloser, error) {
	return buildContextReader(contextDir)
}
