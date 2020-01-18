// +build darwin dragonfly freebsd linux netbsd openbsd solaris

package providers

import (
	"os"
	"testing"
)

func TestConfiguration_TokenFromProcess(t *testing.T) {
	t.Run("token from environment variables", func(t *testing.T) {
		envVar := "CITOP_TEST_TOKEN_FROM_ENV_VAR"
		if err := os.Setenv(envVar, "token"); err != nil {
			t.Fatal(err)
		}

		token, err := token("", []string{"sh", "-c", "echo $" + envVar})
		if err != nil {
			t.Fatal(err)
		}
		if token != "token" {
			t.Fatalf("expected %q but got %q", "token", token)
		}
	})
}
