package api

import (
	"os"
	"testing"
)

func TestDebugNormalizeEndpoint(t *testing.T) {
	os.Setenv("FREEINFERENCE_BASE_URL", "https://freeinference.org/v1")
	id, err := NormalizeEndpoint(os.Getenv("FREEINFERENCE_BASE_URL"))
	t.Logf("NormalizeEndpoint: id=%+v err=%v", id, err)
	if id != nil {
		t.Logf("  IsFI=%v Origin=%s RequestURL=%s", id.IsFI, id.Origin, id.RequestURL)
	}
}
