package cmd

import (
	"fmt"
	"os/exec"
)

// runCommand runs an external command and returns an error if it fails.
func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %s: %w", name, args, string(output), err)
	}
	return nil
}
