package main

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// A real process that exits immediately must still be stoppable after
// waitReady has observed its exit. Previously the exit was delivered as a
// single channel value, so stop blocked forever once waitReady consumed it.
func TestStopReturnsAfterWaitReadyObservesExit(t *testing.T) {
	falseBinary, err := exec.LookPath("false")
	if err != nil {
		t.Skip("false is not available")
	}
	process, err := startServer(falseBinary, "127.0.0.1:1")
	if err != nil {
		t.Fatalf("startServer: %v", err)
	}

	client := newOllamaClient("http://127.0.0.1:1")
	if err := client.waitReady(context.Background(), process, 5*time.Second); err == nil {
		t.Fatal("waitReady succeeded for a process that exited")
	}
	if process.exitErr() == nil {
		t.Fatal("exitErr is nil for a process that exited with status 1")
	}

	stopped := make(chan struct{})
	go func() {
		process.stop()
		process.stop() // repeat calls must not block either
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return after the process had exited")
	}
}

func TestStopTerminatesRunningProcess(t *testing.T) {
	sleepBinary, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep is not available")
	}
	// startServer always passes "serve" as the argument, which sleep rejects,
	// so build the process the same way startServer does.
	process := &serverProcess{cmd: exec.Command(sleepBinary, "60"), done: make(chan struct{})}
	if err := process.cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	go func() {
		process.err = process.cmd.Wait()
		close(process.done)
	}()

	stopped := make(chan struct{})
	go func() { process.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not terminate a running process")
	}
	select {
	case <-process.exited():
	default:
		t.Fatal("exited is not closed after stop returned")
	}
}
