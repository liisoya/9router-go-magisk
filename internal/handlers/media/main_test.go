package media

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Setenv("ALLOW_PRIVATE_NETWORKING", "true")
	os.Exit(m.Run())
}
