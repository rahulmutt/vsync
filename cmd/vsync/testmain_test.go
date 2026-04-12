package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	origXDGStateHome := os.Getenv("XDG_STATE_HOME")
	origVsyncStateDir := os.Getenv("VSYNC_STATE_DIR")
	_ = os.Setenv("XDG_STATE_HOME", "")
	_ = os.Setenv("VSYNC_STATE_DIR", "")
	code := m.Run()
	_ = os.Setenv("XDG_STATE_HOME", origXDGStateHome)
	_ = os.Setenv("VSYNC_STATE_DIR", origVsyncStateDir)
	os.Exit(code)
}
