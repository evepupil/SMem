package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"smem/internal/memory"
	"smem/internal/workspace"
)

const lifecycleWorkerEnv = "SMEM_LIFECYCLE_PROCESS_WORKER"

func TestWindowsConcurrentMutationOldVersion(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("independent process mutation race is required on Windows")
	}

	root := t.TempDir()
	layout := workspace.Layout{
		Home:    filepath.Join(root, "home"),
		Project: filepath.Join(root, "project"),
	}
	for _, dir := range []string{layout.Home, layout.Project} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := memory.AddNote(layout, false, false, "old process-shared version"); err != nil {
		t.Fatal(err)
	}
	records, _, err := memory.List(layout, "project", 0, 100)
	if err != nil || len(records) != 1 {
		t.Fatalf("read seeded record: records=%#v err=%v", records, err)
	}

	gate := filepath.Join(root, "release-gate")
	processes := make([]struct {
		cmd    *exec.Cmd
		cancel context.CancelFunc
		waited bool
	}, 0, 2)
	defer func() {
		_ = os.WriteFile(gate, []byte("release"), 0600)
		for i := range processes {
			process := &processes[i]
			if !process.waited && process.cmd.Process != nil {
				_ = process.cmd.Process.Kill()
				_ = process.cmd.Wait()
				process.waited = true
			}
			process.cancel()
		}
	}()

	operations := []string{"update", "forget"}
	for _, operation := range operations {
		ready := filepath.Join(root, "ready-"+operation)
		result := filepath.Join(root, "result-"+operation)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConcurrentMutationWorker$")
		cmd.Env = append(os.Environ(),
			lifecycleWorkerEnv+"=1",
			"SMEM_LIFECYCLE_HOME="+layout.Home,
			"SMEM_LIFECYCLE_PROJECT="+layout.Project,
			"SMEM_LIFECYCLE_REF="+records[0].Ref,
			"SMEM_LIFECYCLE_OPERATION="+operation,
			"SMEM_LIFECYCLE_READY="+ready,
			"SMEM_LIFECYCLE_GATE="+gate,
			"SMEM_LIFECYCLE_RESULT="+result,
		)
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatalf("start %s worker: %v", operation, err)
		}
		processes = append(processes, struct {
			cmd    *exec.Cmd
			cancel context.CancelFunc
			waited bool
		}{cmd: cmd, cancel: cancel})
	}

	for _, operation := range operations {
		waitForLifecycleFile(t, filepath.Join(root, "ready-"+operation))
	}
	if err := os.WriteFile(gate, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}

	successes := 0
	for i, operation := range operations {
		process := &processes[i]
		if err := process.cmd.Wait(); err != nil {
			t.Fatalf("%s worker process: %v", operation, err)
		}
		process.waited = true
		process.cancel()
		resultPath := filepath.Join(root, "result-"+operation)
		data, err := os.ReadFile(resultPath)
		if err != nil {
			t.Fatalf("read %s worker result: %v", operation, err)
		}
		result := strings.TrimSpace(string(data))
		if result == "success" {
			successes++
		} else if !strings.HasPrefix(result, "error:") {
			t.Fatalf("unexpected %s worker result %q", operation, result)
		}
	}
	if successes != 1 {
		t.Fatalf("exactly one mutation of the old version should succeed, got %d", successes)
	}

	if _, created, err := memory.AddNote(layout, false, false, "post-race lock verification note"); err != nil || !created {
		t.Fatalf("write lock was not usable after child processes: created=%v err=%v", created, err)
	}
}

func TestConcurrentMutationWorker(t *testing.T) {
	if os.Getenv(lifecycleWorkerEnv) != "1" {
		return
	}
	layout := workspace.Layout{
		Home:    os.Getenv("SMEM_LIFECYCLE_HOME"),
		Project: os.Getenv("SMEM_LIFECYCLE_PROJECT"),
	}
	ready := os.Getenv("SMEM_LIFECYCLE_READY")
	gate := os.Getenv("SMEM_LIFECYCLE_GATE")
	resultPath := os.Getenv("SMEM_LIFECYCLE_RESULT")
	if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForLifecycleGate(t, gate)

	var err error
	switch os.Getenv("SMEM_LIFECYCLE_OPERATION") {
	case "update":
		err = memory.Update(layout, os.Getenv("SMEM_LIFECYCLE_REF"), "new process-shared version")
	case "forget":
		err = memory.Forget(layout, os.Getenv("SMEM_LIFECYCLE_REF"))
	default:
		t.Fatalf("unknown worker operation %q", os.Getenv("SMEM_LIFECYCLE_OPERATION"))
	}
	result := "success"
	if err != nil {
		result = "error:" + err.Error()
	}
	if writeErr := os.WriteFile(resultPath, []byte(result), 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
}

func waitForLifecycleFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err := os.Stat(path)
		if err == nil {
			return
		}
		if !os.IsNotExist(err) {
			t.Fatalf("stat synchronization file %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for synchronization file %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForLifecycleGate(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for {
		_, err := os.Stat(path)
		if err == nil {
			return
		}
		if !os.IsNotExist(err) {
			t.Fatalf("stat release gate %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for release gate %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
