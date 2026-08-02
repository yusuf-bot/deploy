package build

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// TarballInfo describes a saved image tarball for reporting.
type TarballInfo struct {
	Version   string
	Path      string
	SizeBytes int64
}

// PruneResult reports what a prune run would do or did.
type PruneResult struct {
	Removed   []TarballInfo
	Kept      []TarballInfo
	Protected []TarballInfo // running-version tarballs, never deleted
	FreedBytes int64
}

// tarballPathFor returns the existing tarball path (zstd or legacy) for a version.
func tarballPathFor(appName, version string) (string, error) {
	for _, ext := range []string{TarballExt, LegacyTarballExt} {
		p := filepath.Join(appImagesDir(appName), version+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

// PruneTarballs deletes the oldest image tarballs for an app, keeping the
// newest `keep` non-protected tarballs. Versions listed in `protected` are
// NEVER deleted (used for the currently-running deployment version).
// When dryRun is true no files are removed and only the plan is returned.
func PruneTarballs(appName string, keep int, protected map[string]bool, dryRun bool) (*PruneResult, error) {
	if keep < 1 {
		return nil, fmt.Errorf("keep must be at least 1, got %d", keep)
	}

	versions, err := ListTarballs(appName)
	if err != nil {
		return nil, err
	}
	// ListTarballs sorts ascending; we want newest-first pruning from the oldest.
	sort.Strings(versions)

	res := &PruneResult{}
	if len(versions) == 0 {
		return res, nil
	}

	// Walk oldest-first and delete non-protected tarballs until at most `keep`
	// remain in total. Protected (currently-running) versions are kept
	// unconditionally and count toward the total, so in the worst case the
	// result is `keep` plus any protected versions that fall outside it.
	toRemove := len(versions) - keep
	if toRemove < 0 {
		toRemove = 0
	}
	removedCount := 0
	for _, v := range versions {
		info, err := tarballInfo(appName, v)
		if err != nil {
			return nil, err
		}
		if protected[v] {
			res.Protected = append(res.Protected, info)
			continue
		}
		if removedCount < toRemove {
			if !dryRun {
				if err := os.Remove(info.Path); err != nil {
					return nil, fmt.Errorf("remove tarball %s: %w", info.Path, err)
				}
			}
			res.Removed = append(res.Removed, info)
			res.FreedBytes += info.SizeBytes
			removedCount++
			continue
		}
		res.Kept = append(res.Kept, info)
	}
	return res, nil
}

// tarballInfo resolves size info for an existing tarball of a version.
func tarballInfo(appName, version string) (TarballInfo, error) {
	p, err := tarballPathFor(appName, version)
	if err != nil {
		return TarballInfo{}, fmt.Errorf("tarball for %s/%s: %w", appName, version, err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return TarballInfo{}, err
	}
	return TarballInfo{Version: version, Path: p, SizeBytes: fi.Size()}, nil
}
