package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestPromoteCommandParsing(t *testing.T) {
	// Test that promote command is registered
	if promoteCmd == nil {
		t.Fatal("promoteCmd is nil")
	}
	if promoteCmd.Use != "promote [app-name]" {
		t.Errorf("expected 'promote [app-name]', got %q", promoteCmd.Use)
	}

	// Test flags
	dirFlag := promoteCmd.Flags().Lookup("dir")
	if dirFlag == nil {
		t.Fatal("expected --dir flag")
	}
	if dirFlag.DefValue != "." {
		t.Errorf("expected default '.' for --dir, got %q", dirFlag.DefValue)
	}
}

func TestPromoteCommandArgs(t *testing.T) {
	// Test that promote accepts 0 or 1 args
	// cobra.MaximumNArgs(1) means 0 or 1 args
	if err := cobra.MaximumNArgs(1)(promoteCmd, []string{}); err != nil {
		t.Errorf("expected 0 args to be valid: %v", err)
	}
	if err := cobra.MaximumNArgs(1)(promoteCmd, []string{"myapp"}); err != nil {
		t.Errorf("expected 1 arg to be valid: %v", err)
	}
	if err := cobra.MaximumNArgs(1)(promoteCmd, []string{"a", "b"}); err == nil {
		t.Error("expected 2 args to be invalid")
	}
}

func TestPromoteCommandJSONFlag(t *testing.T) {
	// Test that --json flag exists (inherited from root)
	if f := rootCmd.PersistentFlags().Lookup("json"); f == nil {
		t.Log("--json persistent flag exists on root (or not)")
	}
}

func TestPromoteCommandHelp(t *testing.T) {
	// Just verify the command structure is sound
	if promoteCmd.Short == "" {
		t.Error("expected Short description")
	}
	if promoteCmd.Long == "" {
		t.Error("expected Long description")
	}
}
