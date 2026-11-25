package main

import (
	"strings"
	"testing"

	"dreame-dashboard/internal/config"
)

func TestProbeRequiresActuate(t *testing.T) {
	if hasFlag(nil, "--yes-actuate") {
		t.Fatal("empty")
	}
	if !hasFlag([]string{"--yes-actuate"}, "--yes-actuate") {
		t.Fatal("flag")
	}
	err := dispatch(config.Config{}, "probe-writes", nil)
	if err == nil || !strings.Contains(err.Error(), "--yes-actuate") {
		t.Fatal(err)
	}
}
