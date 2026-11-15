package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// server is a running Ollama process.
type server interface {
	// exited is closed once the process has exited. Closing (rather than
	// sending a value) lets any number of callers observe the exit.
	exited() <-chan struct{}
	// exitErr returns the process's exit error. It is valid only after
	// exited is closed.
	exitErr() error
	stop()
}

type serverProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// startServer runs `ollama serve` bound to host. Ollama's own logs go to
// stderr; with debug logging off they do not include prompt contents.
func startServer(binary, host string) (server, error) {
	cmd := exec.Command(binary, "serve")
	cmd.Env = serverEnv(os.Environ(), host)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &serverProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		process.err = cmd.Wait()
		close(process.done)
	}()
	return process, nil
}

func (process *serverProcess) exited() <-chan struct{} { return process.done }

func (process *serverProcess) exitErr() error { return process.err }

// stop asks Ollama to shut down and kills it if it does not exit in time.
// It returns immediately if the process has already exited.
func (process *serverProcess) stop() {
	select {
	case <-process.done:
		return
	default:
	}
	_ = process.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-process.done:
	case <-time.After(10 * time.Second):
		_ = process.cmd.Process.Kill()
		<-process.done
	}
}

// serverEnv pins the listen address and removes settings that would log
// prompts or let the environment change where models are loaded from.
func serverEnv(environ []string, host string) []string {
	blocked := map[string]bool{
		"OLLAMA_HOST":    true,
		"OLLAMA_DEBUG":   true,
		"OLLAMA_NOPRUNE": true,
		"OLLAMA_MODELS":  true,
	}
	env := make([]string, 0, len(environ)+4)
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if !blocked[name] {
			env = append(env, entry)
		}
	}
	return append(env,
		"OLLAMA_HOST="+host,
		"OLLAMA_MODELS=/models",
		"OLLAMA_NOPRUNE=1",
	)
}
