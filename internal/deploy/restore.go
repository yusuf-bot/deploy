package deploy

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deploy/internal/audit"
	"deploy/internal/build"
	"deploy/internal/config"
	"deploy/internal/state"
	"deploy/internal/types"

	"github.com/google/uuid"
	moby "github.com/moby/moby/client"
)

// RestoreApp restores a single app from a per-app backup archive created by
// `deploy backup <app>`. Unlike the full-system restore (which requires the
// daemon to be stopped), RestoreApp runs with the daemon up: it extracts the
// archive, imports the app's DB rows in FK-safe order, copies the saved image
// tarballs back into place, loads the image, creates + starts a container,
// health checks it, and regenerates the Caddy config from state. If the
// backup's port is already taken, a fresh port is allocated from the pool.
func (d *Deployer) RestoreApp(ctx context.Context, backupFile, appName string) (*types.PromoteResponse, error) {
	lock, err := d.lockManager.Acquire(appName)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Release()

	startTime := time.Now()

	if _, err := os.Stat(backupFile); err != nil {
		return nil, fmt.Errorf("backup file not found: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "deploy-restore-app-")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := untarGz(backupFile, tmpDir); err != nil {
		return nil, fmt.Errorf("extract backup: %w", err)
	}

	// Per-app payload.
	data, err := os.ReadFile(filepath.Join(tmpDir, "app.json"))
	if err != nil {
		return nil, fmt.Errorf("read app.json from backup: %w (not a per-app backup?)", err)
	}
	var bk state.AppBackupData
	if err := json.Unmarshal(data, &bk); err != nil {
		return nil, fmt.Errorf("parse app.json: %w", err)
	}
	if bk.App.Name != appName {
		return nil, fmt.Errorf("backup is for app %q, not %q", bk.App.Name, appName)
	}

	// Master key used to encrypt secrets in this archive.
	oldKey, err := os.ReadFile(filepath.Join(tmpDir, "master.key"))
	if err != nil {
		return nil, fmt.Errorf("read master.key from backup: %w", err)
	}
	if len(oldKey) != state.KeySize {
		return nil, fmt.Errorf("invalid master.key in backup: got %d bytes, want %d", len(oldKey), state.KeySize)
	}

	// 1. Import DB rows (app -> port_allocations -> secrets -> domains ->
	// deployments), re-encrypting secrets under the current daemon key.
	appID, err := state.ImportApp(d.db, &bk, oldKey, d.masterKey)
	if err != nil {
		return nil, fmt.Errorf("import app data: %w", err)
	}

	// 2. Copy saved image tarballs into ~/.deploy/images/<app>/ so
	// build.OpenTarball can find them.
	srcImages := filepath.Join(tmpDir, "images", appName)
	if _, err := os.Stat(srcImages); err == nil {
		dstImages := filepath.Join(config.DeployDirPath(), "images", appName)
		if err := copyDirTo(srcImages, dstImages); err != nil {
			return nil, fmt.Errorf("copy image tarballs: %w", err)
		}
	}

	// 3. Determine the version to restore: the active deployment's version
	// when a matching tarball exists, otherwise the newest saved tarball.
	versions, err := build.ListTarballs(appName)
	if err != nil {
		return nil, fmt.Errorf("list tarballs: %w", err)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("backup contains no saved image for %q", appName)
	}
	version := versions[len(versions)-1]
	if active, aerr := state.GetActiveDeployment(d.db, appID); aerr == nil && active != nil {
		for _, v := range versions {
			if v == active.Version {
				version = v
				break
			}
		}
	}

	// 4. Load the image from the tarball.
	tarball, err := build.OpenTarball(appName, version)
	if err != nil {
		return nil, err
	}
	defer tarball.Close()
	imageRef := fmt.Sprintf("%s:%s", appName, version)
	loadResult, err := d.client.ImageLoad(ctx, tarball, moby.ImageLoadWithQuiet(true))
	if err != nil {
		return nil, fmt.Errorf("load image: %w", err)
	}
	io.Copy(io.Discard, loadResult)
	loadResult.Close()

	// 5. Re-read the (freshly imported) app row.
	app, err := state.GetAppByName(d.db, appName)
	if err != nil {
		return nil, fmt.Errorf("get app: %w", err)
	}
	if app == nil {
		return nil, fmt.Errorf("app %q not found after import", appName)
	}

	// 6. Port: keep the backup port when it is free, otherwise reallocate.
	hostPort, err := d.restorePort(appName, bk.PortAllocation)
	if err != nil {
		return nil, err
	}

	// 7. Merge env (deploy.yml env in app row < group env < secrets).
	secrets, err := state.ListSecretsByApp(d.db, appID, d.masterKey)
	if err != nil {
		secrets = nil
	}
	// Get group env if app belongs to a group
	var groupEnv map[string]string
	if app.GroupID != nil {
		groupEnv, _ = state.GetGroupEnv(d.db, *app.GroupID)
	}
	mergedEnv := state.MergeEnv(app.Env, groupEnv, secrets)

	// 8. Create deployment record.
	depID := uuid.New().String()
	dep := &types.Deployment{
		ID:      depID,
		AppID:   appID,
		Version: version,
		Status:  types.DeployStatusDeploying,
		Port:    hostPort,
	}
	if _, err := state.CreateDeployment(d.db, dep); err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}

	// 9. Create + start the container on the restored port.
	svcPort := hostPort
	if app.ServicePort > 0 {
		svcPort = app.ServicePort
	}
	resources := types.ResourceConfig{}
	if app.Resources != nil {
		resources = *app.Resources
	}
	containerID, err := d.createPromoteContainer(ctx, app, imageRef, mergedEnv, hostPort, svcPort, version, resources, app.Network)
	if err != nil {
		state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed, fmt.Sprintf("create container: %v", err))
		return nil, fmt.Errorf("create container: %w", err)
	}
	if err := d.runner.StartContainer(ctx, containerID); err != nil {
		d.runner.RemoveContainer(ctx, containerID)
		state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed, fmt.Sprintf("start container: %v", err))
		return nil, fmt.Errorf("start container: %w", err)
	}

	// 10. Health check. No deploy.yml is available for a restore, so use the
	// defaults ("/" path, 1s initial delay, 2s interval, 5s timeout, 15 retries).
	log.Printf("health checking restored container %s on port %d...", containerID[:12], hostPort)
	if err := d.runner.HealthCheck(ctx, containerID, hostPort, "/", 1*time.Second, 2*time.Second, 5*time.Second, 15); err != nil {
		d.runner.StopContainer(ctx, containerID)
		d.runner.RemoveContainer(ctx, containerID)
		state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed, fmt.Sprintf("health check: %v", err))
		return nil, fmt.Errorf("health check: %w", err)
	}

	// 11. Update the app record and mark the deployment active (atomic).
	tx, err := d.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := state.UpdateAppContainer(tx, appName, containerID); err != nil {
		return nil, fmt.Errorf("update app container: %w", err)
	}
	if err := state.UpdateAppPort(tx, appName, hostPort); err != nil {
		return nil, fmt.Errorf("update app port: %w", err)
	}
	if err := state.UpdateAppServicePort(tx, appName, svcPort); err != nil {
		return nil, fmt.Errorf("update app service port: %w", err)
	}
	if err := state.UpdateAppResources(tx, appName, app.Resources); err != nil {
		return nil, fmt.Errorf("update app resources: %w", err)
	}
	if err := state.UpdateAppStatus(tx, appName, types.StatusRunning); err != nil {
		return nil, fmt.Errorf("update app status: %w", err)
	}
	if err := state.UpdateDeploymentStatus(tx, depID, types.DeployStatusActive, ""); err != nil {
		return nil, fmt.Errorf("update deployment status: %w", err)
	}
	if err := state.SetActiveDeployment(tx, dep); err != nil {
		log.Printf("warning: set active deployment: %v", err)
	}
	if err := state.DeactivateOtherDeployments(tx, appID, depID); err != nil {
		log.Printf("warning: deactivate other deployments: %v", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	// 12. Update port allocation.
	if err := state.UpdatePortAllocation(d.db, appName, hostPort); err != nil {
		log.Printf("warning: update port allocation: %v", err)
	}

	// 13. Regenerate every Caddy site snippet from the app's actual port/status
	// in the DB (domains were imported above), so snippets point at the
	// restored port — including after a port reallocation.
	if d.caddyManager != nil && d.caddyManager.IsRunning() {
		if err := d.caddyManager.Reload(); err != nil {
			return nil, fmt.Errorf("caddy reload from state: %w", err)
		}
	}

	log.Printf("restored %s to %s (container=%s, port=%d)", appName, version, containerID[:12], hostPort)
	audit.Log(audit.Entry{
		Time:        time.Now().UTC(),
		Action:      "restore",
		App:         appName,
		Version:     version,
		DurationMs:  time.Since(startTime).Milliseconds(),
		Result:      "ok",
		InitiatedBy: audit.CurrentUser(),
	})
	return &types.PromoteResponse{
		Message:        fmt.Sprintf("restored %s to version %s on port %d", appName, version, hostPort),
		Version:        version,
		NewContainerID: containerID,
		Port:           hostPort,
	}, nil
}

// restorePort keeps the backup port when it is free (not owned by another app
// and not bound on the host); otherwise it releases any stale allocation and
// allocates a fresh port from the pool.
func (d *Deployer) restorePort(appName string, backupPort int) (int, error) {
	free, err := d.portIsFree(appName, backupPort)
	if err != nil {
		return 0, err
	}
	if free {
		return backupPort, nil
	}
	log.Printf("port %d from backup is taken; allocating a fresh port for %q", backupPort, appName)
	if err := state.ReleasePort(d.db, appName); err != nil {
		return 0, fmt.Errorf("release port: %w", err)
	}
	newPort, err := state.AllocatePort(d.db, appName, d.checkPortInUse)
	if err != nil {
		return 0, fmt.Errorf("allocate port: %w", err)
	}
	return newPort, nil
}

// portIsFree reports whether the given host port is not allocated to another
// app in the DB and is not bound by anything on the host.
func (d *Deployer) portIsFree(appName string, port int) (bool, error) {
	var owner string
	err := d.db.QueryRow("SELECT app_name FROM port_allocations WHERE port = ?", port).Scan(&owner)
	if err == nil && owner != appName {
		return false, nil // another app owns this port
	}
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("check port allocation: %w", err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false, nil // bound on the host (e.g. a running container)
	}
	ln.Close()
	return true, nil
}

// checkPortInUse reports whether a host port is bound, matching the signature
// expected by state.AllocatePort.
func (d *Deployer) checkPortInUse(port int) (bool, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true, nil // port bound on the host
	}
	ln.Close()
	return false, nil
}

// untarGz extracts a tar.gz archive into dst, refusing to write outside dst.
func untarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		path := filepath.Join(dst, hdr.Name)
		rel, err := filepath.Rel(dst, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}

// copyDirTo recursively copies a directory from src to dst.
func copyDirTo(src, dst string) error {
	if err := os.MkdirAll(dst, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDirTo(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		in, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			in.Close()
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			out.Close()
			return err
		}
		in.Close()
		out.Close()
	}
	return nil
}
