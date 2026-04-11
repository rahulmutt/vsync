package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	origXDGStateDir := os.Getenv("XDG_STATE_DIR")
	origVsyncStateDir := os.Getenv("VSYNC_STATE_DIR")
	_ = os.Setenv("XDG_STATE_DIR", "")
	_ = os.Setenv("VSYNC_STATE_DIR", "")
	code := m.Run()
	_ = os.Setenv("XDG_STATE_DIR", origXDGStateDir)
	_ = os.Setenv("VSYNC_STATE_DIR", origVsyncStateDir)
	os.Exit(code)
}
