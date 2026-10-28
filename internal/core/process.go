package core

import (
	"errors"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// clock is the time source. Tests replace it to drive timeouts.
type clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// process is a started server in its own process group.
type process interface {
	Pid() int
	// Signal sends sig to the whole process group.
	Signal(sig syscall.Signal) error
	// Wait blocks until the process exits and returns its exit code
	// (128+signal when it was killed by a signal). It is called once.
	Wait() int
}

// starter spawns server processes.
type starter interface {
	// Start runs argv with env added to the current environment, in a new
	// process group, with stdout and stderr copied to the writers.
	Start(argv, env []string, stdout, stderr io.Writer) (process, error)
}

// waitDelay bounds how long Wait waits for the output pipes after the
// process exits: a grandchild that inherited stderr may hold them open.
const waitDelay = 2 * time.Second

type execStarter struct{}

func (execStarter) Start(argv, env []string, stdout, stderr io.Writer) (process, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty server command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env // the engine builds the complete environment
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{cmd: cmd}, nil
}

type execProcess struct{ cmd *exec.Cmd }

func (p *execProcess) Pid() int { return p.cmd.Process.Pid }

// Signal targets the group: with Setpgid the group id is the pid.
func (p *execProcess) Signal(sig syscall.Signal) error {
	return syscall.Kill(-p.cmd.Process.Pid, sig)
}

func (p *execProcess) Wait() int {
	_ = p.cmd.Wait()
	st, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		return p.cmd.ProcessState.ExitCode()
	}
	if st.Signaled() {
		return 128 + int(st.Signal())
	}
	return st.ExitStatus()
}

// signalPID sends sig to one process; sig 0 checks that it is alive.
func signalPID(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }
