package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestSecretsCommandRegistered(t *testing.T) {
	if secretsCmd == nil {
		t.Fatal("secretsCmd is nil")
	}
	if secretsCmd.Use != "secrets" {
		t.Errorf("expected 'secrets', got %q", secretsCmd.Use)
	}
}

func TestSecretsSubcommands(t *testing.T) {
	expected := map[string]bool{"set": true, "get": true, "rm": true, "ls": true}
	for _, sub := range secretsCmd.Commands() {
		if !expected[sub.Name()] {
			t.Errorf("unexpected subcommand %q", sub.Name())
		}
		delete(expected, sub.Name())
	}
	for name := range expected {
		t.Errorf("missing subcommand %q", name)
	}
}

func TestSecretsSetArgValidation(t *testing.T) {
	// set requires exactly 2 args
	if err := cobra.ExactArgs(2)(secretsSetCmd, []string{"app", "KEY=VALUE"}); err != nil {
		t.Errorf("expected 2 args to be valid: %v", err)
	}
	if err := cobra.ExactArgs(2)(secretsSetCmd, []string{"app"}); err == nil {
		t.Error("expected 1 arg to be invalid")
	}
}

func TestSecretsGetArgValidation(t *testing.T) {
	if err := cobra.ExactArgs(2)(secretsGetCmd, []string{"app", "mykey"}); err != nil {
		t.Errorf("expected 2 args to be valid: %v", err)
	}
}

func TestSecretsRmArgValidation(t *testing.T) {
	if err := cobra.ExactArgs(2)(secretsRmCmd, []string{"app", "mykey"}); err != nil {
		t.Errorf("expected 2 args to be valid: %v", err)
	}
}

func TestSecretsLsArgValidation(t *testing.T) {
	if err := cobra.ExactArgs(1)(secretsLsCmd, []string{"app"}); err != nil {
		t.Errorf("expected 1 arg to be valid: %v", err)
	}
}

func TestSecretsGetRawFlag(t *testing.T) {
	f := secretsGetCmd.Flags().Lookup("raw")
	if f == nil {
		t.Fatal("expected --raw flag on secrets get")
	}
	if f.DefValue != "false" {
		t.Errorf("expected --raw default false, got %q", f.DefValue)
	}
}

func TestSecretsCommandHelp(t *testing.T) {
	if secretsCmd.Short == "" {
		t.Error("expected Short description")
	}
}
