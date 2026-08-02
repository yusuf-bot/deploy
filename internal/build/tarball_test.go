package build

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/moby/moby/api/types/image"
	moby "github.com/moby/moby/client"
)

// mockDocker implements dockerClient for testing.
type mockDocker struct {
	mu          sync.Mutex
	savedImages map[string][]byte // imageRef -> tar data
	loadedRefs  []string
	buildCalls  int
	shouldFail  map[string]error
}

func newMockDocker() *mockDocker {
	return &mockDocker{
		savedImages: make(map[string][]byte),
		shouldFail:  make(map[string]error),
	}
}

func (m *mockDocker) addFail(method string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldFail[method] = err
}

func (m *mockDocker) failIf(method string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err, ok := m.shouldFail[method]; ok {
		return err
	}
	return nil
}

func (m *mockDocker) ImageBuild(ctx context.Context, buildContext io.Reader, opts moby.ImageBuildOptions) (moby.ImageBuildResult, error) {
	if err := m.failIf("ImageBuild"); err != nil {
		return moby.ImageBuildResult{}, err
	}
	m.mu.Lock()
	m.buildCalls++
	m.mu.Unlock()
	// Read and discard build context
	io.Copy(io.Discard, buildContext)
	// Simulate a successful build
	ref := opts.Tags[0]
	m.mu.Lock()
	m.savedImages[ref] = []byte("dummy-image-data")
	m.mu.Unlock()
	return moby.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (m *mockDocker) ImageSave(ctx context.Context, images []string, opts ...moby.ImageSaveOption) (moby.ImageSaveResult, error) {
	if err := m.failIf("ImageSave"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ref := images[0]
	data, ok := m.savedImages[ref]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return &mockReadCloser{Reader: bytes.NewReader(data)}, nil
}

func (m *mockDocker) ImageLoad(ctx context.Context, input io.Reader, opts ...moby.ImageLoadOption) (moby.ImageLoadResult, error) {
	if err := m.failIf("ImageLoad"); err != nil {
		return nil, err
	}
	data, _ := io.ReadAll(input)
	m.mu.Lock()
	m.loadedRefs = append(m.loadedRefs, string(data))
	m.mu.Unlock()
	return &mockReadCloser{Reader: strings.NewReader("loaded")}, nil
}

func (m *mockDocker) ImageInspect(ctx context.Context, imageID string, opts ...moby.ImageInspectOption) (moby.ImageInspectResult, error) {
	if err := m.failIf("ImageInspect"); err != nil {
		return moby.ImageInspectResult{}, err
	}
	return moby.ImageInspectResult{
		InspectResponse: image.InspectResponse{
			ID: "sha256:abc123def456",
		},
	}, nil
}

type mockReadCloser struct {
	io.Reader
}

func (m *mockReadCloser) Close() error { return nil }

func TestSaveImage(t *testing.T) {
	mock := newMockDocker()
	ctx := context.Background()
	appName := "test-app"
	version := "v1.0.0"
	imageRef := "test-app:v1.0.0"

	// Pre-save an image in the mock
	mock.mu.Lock()
	mock.savedImages[imageRef] = []byte("test-tar-data")
	mock.mu.Unlock()

	path, err := SaveImage(ctx, mock, imageRef, appName, version)
	if err != nil {
		t.Fatalf("SaveImage: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("saved tarball not found at %s", path)
	}
	// Verify the file is a valid zstd stream and decompresses to the original data
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open tarball: %v", err)
	}
	defer f.Close()
	dec, err := zstd.NewReader(f)
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer dec.Close()
	data, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("read tarball: %v", err)
	}
	if string(data) != "test-tar-data" {
		t.Errorf("expected test-tar-data, got %s", string(data))
	}

	// Verify path format
	if !strings.HasSuffix(path, ".tar.zst") {
		t.Errorf("expected .tar.zst extension, got %s", path)
	}
	if !strings.Contains(path, appName) {
		t.Errorf("expected path to contain app name %s", appName)
	}
}

func TestSaveImageAtomicWrite(t *testing.T) {
	mock := newMockDocker()
	ctx := context.Background()
	imageRef := "myapp:v1"
	mock.mu.Lock()
	mock.savedImages[imageRef] = []byte("atomic-data")
	mock.mu.Unlock()

	path, err := SaveImage(ctx, mock, imageRef, "atomic-app", "v1")
	if err != nil {
		t.Fatalf("SaveImage: %v", err)
	}

	// Ensure no .tmp file remains
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("temp file %s should have been removed", tmpPath)
	}
}

func TestLoadImage(t *testing.T) {
	mock := newMockDocker()
	ctx := context.Background()
	appName := "load-app"
	version := "v1.0.0"
	imageRef := "load-app:v1.0.0"

	mock.mu.Lock()
	mock.savedImages[imageRef] = []byte("loadable-data")
	mock.mu.Unlock()

	// First save
	_, err := SaveImage(ctx, mock, imageRef, appName, version)
	if err != nil {
		t.Fatalf("SaveImage: %v", err)
	}

	// Clear loaded refs
	mock.mu.Lock()
	mock.loadedRefs = nil
	mock.mu.Unlock()

	// Now load
	ref, err := LoadImage(ctx, mock, appName, version)
	if err != nil {
		t.Fatalf("LoadImage: %v", err)
	}
	if ref != "load-app:v1.0.0" {
		t.Errorf("expected load-app:v1.0.0, got %s", ref)
	}
}

func TestRemoveTarball(t *testing.T) {
	mock := newMockDocker()
	ctx := context.Background()
	imageRef := "rm-app:v1"
	mock.mu.Lock()
	mock.savedImages[imageRef] = []byte("data")
	mock.mu.Unlock()

	path, err := SaveImage(ctx, mock, imageRef, "rm-app", "v1")
	if err != nil {
		t.Fatalf("SaveImage: %v", err)
	}

	if err := RemoveTarball("rm-app", "v1"); err != nil {
		t.Fatalf("RemoveTarball: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("tarball should have been removed")
	}
}

func TestListTarballs(t *testing.T) {
	mock := newMockDocker()
	ctx := context.Background()
	appName := "list-app"
	versions := []string{"v1", "v2", "v3", "v4", "v5"}

	for _, v := range versions {
		mock.mu.Lock()
		mock.savedImages["list-app:"+v] = []byte(v)
		mock.mu.Unlock()
		if _, err := SaveImage(ctx, mock, "list-app:"+v, appName, v); err != nil {
			t.Fatalf("SaveImage %s: %v", v, err)
		}
	}

	list, err := ListTarballs(appName)
	if err != nil {
		t.Fatalf("ListTarballs: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("expected 5 tarballs, got %d", len(list))
	}
	if list[0] != "v1" {
		t.Errorf("expected first to be v1, got %s", list[0])
	}
}

func TestListTarballsNoDir(t *testing.T) {
	versions, err := ListTarballs("nonexistent-app")
	if err != nil {
		t.Fatalf("ListTarballs: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("expected empty list, got %d", len(versions))
	}
}

func TestCleanOldTarballs(t *testing.T) {
	mock := newMockDocker()
	ctx := context.Background()
	appName := "clean-app"

	// Save 8 tarballs
	for i := 1; i <= 8; i++ {
		v := fmt.Sprintf("v%d", i)
		imageRef := "clean-app:" + v
		mock.mu.Lock()
		mock.savedImages[imageRef] = []byte(v)
		mock.mu.Unlock()
		if _, err := SaveImage(ctx, mock, imageRef, appName, v); err != nil {
			t.Fatalf("SaveImage %s: %v", v, err)
		}
	}

	// Keep last 5
	if err := CleanOldTarballs(appName, 5); err != nil {
		t.Fatalf("CleanOldTarballs: %v", err)
	}

	list, err := ListTarballs(appName)
	if err != nil {
		t.Fatalf("ListTarballs: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("expected 5 tarballs after cleanup, got %d", len(list))
	}
	if list[0] != "v4" {
		t.Errorf("expected first to be v4 (oldest kept), got %s", list[0])
	}
}

func TestTarballPath(t *testing.T) {
	p := TarballPath("myapp", "v1.0.0")
	if !strings.HasSuffix(p, ".tar.zst") {
		t.Errorf("expected .tar.zst extension, got %s", p)
	}
	if !strings.Contains(p, "myapp") {
		t.Errorf("expected path to contain app name")
	}
	if !strings.Contains(p, "v1.0.0") {
		t.Errorf("expected path to contain version")
	}
}

func TestLoadImageLegacyTar(t *testing.T) {
	mock := newMockDocker()
	ctx := context.Background()
	appName := "legacy-app"
	version := "v0.9.0"

	// Write a legacy uncompressed .tar manually
	dir := appImagesDir(appName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacyPath := filepath.Join(dir, version+".tar")
	if err := os.WriteFile(legacyPath, []byte("legacy-tar-data"), 0600); err != nil {
		t.Fatalf("write legacy tarball: %v", err)
	}

	// LoadImage must read the legacy format
	ref, err := LoadImage(ctx, mock, appName, version)
	if err != nil {
		t.Fatalf("LoadImage legacy: %v", err)
	}
	if ref != "legacy-app:v0.9.0" {
		t.Errorf("expected legacy-app:v0.9.0, got %s", ref)
	}

	mock.mu.Lock()
	loaded := len(mock.loadedRefs) > 0 && mock.loadedRefs[len(mock.loadedRefs)-1] == "legacy-tar-data"
	mock.mu.Unlock()
	if !loaded {
		t.Errorf("legacy tar data was not loaded verbatim")
	}

	// ListTarballs must include the legacy version
	versions, err := ListTarballs(appName)
	if err != nil {
		t.Fatalf("ListTarballs: %v", err)
	}
	if len(versions) != 1 || versions[0] != version {
		t.Errorf("expected [%s] from legacy tar, got %v", version, versions)
	}
}

func TestListTarballsMixedFormats(t *testing.T) {
	mock := newMockDocker()
	ctx := context.Background()
	appName := "mixed-app"

	// One zstd tarball via SaveImage
	mock.mu.Lock()
	mock.savedImages["mixed-app:v2"] = []byte("v2-data")
	mock.mu.Unlock()
	if _, err := SaveImage(ctx, mock, "mixed-app:v2", appName, "v2"); err != nil {
		t.Fatalf("SaveImage: %v", err)
	}

	// One legacy .tar written manually
	dir := appImagesDir(appName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "v1.tar"), []byte("v1-data"), 0600); err != nil {
		t.Fatalf("write legacy tarball: %v", err)
	}

	versions, err := ListTarballs(appName)
	if err != nil {
		t.Fatalf("ListTarballs: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %v", versions)
	}
	if versions[0] != "v1" || versions[1] != "v2" {
		t.Errorf("expected [v1 v2], got %v", versions)
	}

	// RemoveTarball must remove both formats for a version
	if err := RemoveTarball(appName, "v1"); err != nil {
		t.Fatalf("RemoveTarball v1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "v1.tar")); !os.IsNotExist(err) {
		t.Errorf("legacy v1.tar should have been removed")
	}
}

func TestRemoveTarballRemovesZstd(t *testing.T) {
	mock := newMockDocker()
	ctx := context.Background()
	appName := "rmz-app"
	version := "v1"

	mock.mu.Lock()
	mock.savedImages["rmz-app:v1"] = []byte("data")
	mock.mu.Unlock()
	path, err := SaveImage(ctx, mock, "rmz-app:v1", appName, version)
	if err != nil {
		t.Fatalf("SaveImage: %v", err)
	}

	if err := RemoveTarball(appName, version); err != nil {
		t.Fatalf("RemoveTarball: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("tarball should have been removed")
	}
}

func TestSaveImageError(t *testing.T) {
	mock := newMockDocker()
	mock.addFail("ImageSave", io.ErrClosedPipe)
	ctx := context.Background()

	_, err := SaveImage(ctx, mock, "fail:v1", "fail-app", "v1")
	if err == nil {
		t.Fatal("expected error from ImageSave")
	}
}

func TestLoadImageError(t *testing.T) {
	mock := newMockDocker()
	_, err := LoadImage(context.Background(), mock, "no-such-app", "v1")
	if err == nil {
		t.Fatal("expected error for missing tarball")
	}
}
