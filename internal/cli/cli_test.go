package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"smem/internal/workspace"
)

func TestRejectInvalidCommands(t *testing.T) {
	layout := workspace.Layout{Home: t.TempDir()}
	for _, args := range [][]string{{"erase"}, {"init", "--agents", "unknown"}, {"init", "typo"}, {"search"}, {"add", "--wrong", "note"}, {"migrate", "now"}} {
		if err := Run(args, layout, io.Discard, io.Discard); err == nil {
			t.Errorf("expected error: %v", args)
		}
	}
}

func TestMigrationPrintsInstructionsOnly(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"migrate"}, workspace.Layout{}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mem search", "mem add --global", "等我确认", "不修改、删除", "密钥"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
