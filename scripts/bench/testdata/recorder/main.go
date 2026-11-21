package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	self, err := os.Executable()
	if err != nil {
		os.Exit(125)
	}
	command := exec.Command(filepath.Join(filepath.Dir(self), "payload"+filepath.Ext(self)), os.Args[1:]...)
	var output bytes.Buffer
	command.Stdout, command.Stderr, command.Stdin = &output, os.Stderr, os.Stdin
	start := time.Now()
	err = command.Run()
	code := 0
	if err != nil {
		code = 1
		if failure, ok := err.(*exec.ExitError); ok {
			code = failure.ExitCode()
		}
	}
	_, _ = os.Stdout.Write(output.Bytes())
	for dir, _ := os.Getwd(); dir != ""; dir = filepath.Dir(dir) {
		folder := filepath.Join(dir, ".smem-observations")
		if info, statErr := os.Stat(folder); statErr == nil && info.IsDir() {
			file, openErr := os.CreateTemp(folder, "call-*.json")
			if openErr != nil {
				os.Exit(125)
			}
			encodeErr := json.NewEncoder(file).Encode(map[string]any{"args": os.Args[1:], "code": code, "output": output.String(), "time": start.UnixMilli()})
			if closeErr := file.Close(); encodeErr != nil || closeErr != nil {
				os.Exit(125)
			}
			break
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	os.Exit(code)
}
