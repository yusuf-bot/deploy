package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeployDirPathPrecedence(t *testing.T) {
	// No env set: default <HomeDir>/.deploy
	os.Unsetenv("DEPLOY_DATA_DIR")
	os.Unsetenv("DEPLOY_HOME")
	if got, want := DeployDirPath(), filepath.Join(HomeDir(), DeployDir); got != want {
		t.Errorf("default: got %q, want %q", got, want)
	}

	// Legacy DEPLOY_HOME honored when DEPLOY_DATA_DIR unset
	t.Setenv("DEPLOY_HOME", "/legacy/.deploy")
	os.Unsetenv("DEPLOY_DATA_DIR")
	if got, want := DeployDirPath(), "/legacy/.deploy"; got != want {
		t.Errorf("DEPLOY_HOME: got %q, want %q", got, want)
	}

	// DEPLOY_DATA_DIR wins over DEPLOY_HOME
	t.Setenv("DEPLOY_DATA_DIR", "/mnt/bigvolume/.deploy")
	if got, want := DeployDirPath(), "/mnt/bigvolume/.deploy"; got != want {
		t.Errorf("DEPLOY_DATA_DIR: got %q, want %q", got, want)
	}
}
