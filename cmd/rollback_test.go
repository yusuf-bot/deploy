package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestRollbackCommandParsing(t *testing.T) {
	if rollbackCmd == nil {
		t.Fatal("rollbackCmd is nil")
	}
	if rollbackCmd.Use != "rollback <app-name> [version]" {
		t.Errorf("expected 'rollback <app-name> [version]', got %q", rollbackCmd.Use)
	}

	// Test RangeArgs(1,2): should accept 1 or 2 args
	if err := cobra.RangeArgs(1, 2)(rollbackCmd, []string{"myapp"}); err != nil {
		t.Errorf("expected 1 arg to be valid: %v", err)
	}
	if err := cobra.RangeArgs(1, 2)(rollbackCmd, []string{"myapp", "v1.0.0"}); err != nil {
		t.Errorf("expected 2 args to be valid: %v", err)
	}
	if err := cobra.RangeArgs(1, 2)(rollbackCmd, []string{}); err == nil {
		t.Error("expected 0 args to be invalid")
	}
	if err := cobra.RangeArgs(1, 2)(rollbackCmd, []string{"a", "b", "c"}); err == nil {
		t.Error("expected 3 args to be invalid")
	}
}

func TestRollbackCommandHelp(t *testing.T) {
	if rollbackCmd.Short == "" {
		t.Error("expected Short description")
	}
	if rollbackCmd.Long == "" {
		t.Error("expected Long description")
	}
}
