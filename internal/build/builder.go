package build

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	// Load .dockerignore if present
	ignorePatterns := loadDockerIgnore(dir)

	// Auto-exclude common patterns
	autoExclude := []string{
		"node_modules", ".git", ".hg", ".bzr", ".svn",
		"*.log", "*.db", "*.sqlite", "*.sqlite3",
		".cache", "tmp", ".tmp", "*.tmp",
		".DS_Store", "Thumbs.db",
		".env", ".env.local",
	}
	// Merge auto-exclude with .dockerignore patterns. User patterns are
	// evaluated first so an explicit negation (e.g. `!.env`) re-includes a
	// file that would otherwise be auto-excluded.
	allPatterns := ignorePatterns
	if allPatterns == nil {
		allPatterns = []string{}
	}
	allPatterns = append(allPatterns, autoExclude...)

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

		if isExcluded(relPath, fi.IsDir(), allPatterns) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Handle symlinks: read link target, create header, no content
		if fi.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
			header, err := tar.FileInfoHeader(fi, linkTarget)
			if err != nil {
				return fmt.Errorf("tar header for %s: %w", relPath, err)
			}
			header.Name = filepath.ToSlash(relPath)
			if err := tw.WriteHeader(header); err != nil {
				return fmt.Errorf("write tar header for %s: %w", relPath, err)
			}
			return nil // symlinks have no content to copy
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
			written, err := io.Copy(tw, io.LimitReader(f, header.Size))
			if err != nil {
				return fmt.Errorf("copy %s: %w", path, err)
			}
			if written < header.Size {
				// File shrunk between stat and read — this is unusual
				// but not fatal. Continue with what we have.
				log.Printf("warning: file %s changed size while reading (%d < %d)", path, written, header.Size)
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

	// Parse the build response — captures output and detects build errors
	if err := parseBuildResponse(resp.Body); err != nil {
		return nil, err
	}

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

// BuildConfig holds parameters for a single Docker image build.
type BuildConfig struct {
	ImageRef   string
	ContextDir string
	Dockerfile string
	BuildArgs  map[string]string
	Target     string
}

// BuildFromConfig performs a build using the given configuration.
// This is the single code path for all image builds — ensures consistent
// handling of build args, dockerignore, and multi-stage builds.
func (b *Builder) BuildFromConfig(ctx context.Context, cfg BuildConfig) (*types.BuildResult, error) {
	// Create build context
	buildCtx, err := CreateBuildContext(cfg.ContextDir, cfg.Dockerfile)
	if err != nil {
		return nil, fmt.Errorf("create build context: %w", err)
	}
	defer buildCtx.Close()

	// Build args: convert map[string]string to map[string]*string
	buildArgs := make(map[string]*string)
	for k, v := range cfg.BuildArgs {
		val := v
		buildArgs[k] = &val
	}

	opts := moby.ImageBuildOptions{
		Tags:       []string{cfg.ImageRef},
		Dockerfile: cfg.Dockerfile,
		BuildArgs:  buildArgs,
		Target:     cfg.Target,
		Remove:     true,
		ForceRemove: true,
	}

	resp, err := b.client.ImageBuild(ctx, buildCtx, opts)
	if err != nil {
		return nil, fmt.Errorf("docker build: %w", err)
	}
	defer resp.Body.Close()

	// Parse the build response
	if err := parseBuildResponse(resp.Body); err != nil {
		return nil, err
	}

	// Inspect image to get digest
	imageID, err := getImageDigest(ctx, b.client, cfg.ImageRef)
	if err != nil {
		return nil, fmt.Errorf("get image digest: %w", err)
	}

	// Save tarball
	appName := strings.Split(cfg.ImageRef, ":")[0]
	version := strings.Split(cfg.ImageRef, ":")[1]
	tarballPath, err := SaveImage(ctx, b.client, cfg.ImageRef, appName, version)
	if err != nil {
		return nil, fmt.Errorf("save image: %w", err)
	}

	return &types.BuildResult{
		Version:     version,
		ImageRef:    cfg.ImageRef,
		TarballPath: tarballPath,
		ImageDigest: imageID,
	}, nil
}

// parseBuildResponse reads a Docker build JSON stream. Returns a BuildError
// with captured output if the build fails.
func parseBuildResponse(body io.ReadCloser) error {
	defer body.Close()
	var buildOutput strings.Builder
	decoder := json.NewDecoder(body)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := decoder.Decode(&msg); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("parse build output: %w", err)
		}
		if msg.Stream != "" {
			buildOutput.WriteString(msg.Stream)
		}
		if msg.Error != "" {
			return &types.BuildError{
				Code:    types.ErrBuild,
				Message: "docker build failed",
				Detail:  buildOutput.String(),
			}
		}
	}
	return nil
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

// loadDockerIgnore reads .dockerignore from the context directory.
// Returns nil if the file doesn't exist.
func loadDockerIgnore(dir string) []string {
	path := filepath.Join(dir, ".dockerignore")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// isExcluded checks if a file/dir matches any .dockerignore pattern.
func isExcluded(relPath string, isDir bool, patterns []string) bool {
	rel := filepath.ToSlash(relPath)
	base := filepath.Base(rel)

	for _, p := range patterns {
		// Handle negation
		negate := false
		if strings.HasPrefix(p, "!") {
			negate = true
			p = p[1:]
		}

		// Directory-only pattern (trailing /)
		dirOnly := false
		if strings.HasSuffix(p, "/") {
			dirOnly = true
			p = strings.TrimSuffix(p, "/")
		}
		if dirOnly && !isDir {
			continue
		}

		// Clean leading ./
		p = strings.TrimPrefix(p, "./")

		matched := false

		// Match against full relative path
		if m, _ := filepath.Match(p, rel); m {
			matched = true
		}
		// Match against basename
		if !matched {
			if m, _ := filepath.Match(p, base); m {
				matched = true
			}
		}
		// For bare names without wildcards, check each path component
		if !matched && !strings.ContainsAny(p, "*?[") {
			for _, part := range strings.Split(rel, "/") {
				if part == p {
					matched = true
					break
				}
			}
		}

		if matched {
			return !negate
		}
	}
	return false
}
