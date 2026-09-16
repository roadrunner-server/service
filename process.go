package service

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/roadrunner-server/pool/v2/process"
)

// Process holds one execution and its configuration snapshot.
type Process struct {
	command *exec.Cmd
	pid     int64

	// logger
	log     *slog.Logger
	service *Service
	cancel  context.CancelFunc

	done   chan struct{}
	onExit func(*Process)
}

// NewServiceProcess constructs service process structure
func NewServiceProcess(service *Service, name string, l *slog.Logger) *Process {
	snapshot := service.clone()
	log := l
	if snapshot.UseServiceName {
		log = l.With("service", name)
	}

	return &Process{
		service: &snapshot,
		log:     log,
		done:    make(chan struct{}),
	}
}

// write a message to the log (stderr)
func (p *Process) Write(b []byte) (int, error) {
	p.log.Info(string(bytes.TrimSpace(b)))
	return len(b), nil
}

func (p *Process) start() error {
	cmdArgs := strings.Split(p.service.Command, " ")

	if p.service.ExecTimeout > 0 {
		p.createProcessCtx(cmdArgs)
	} else {
		p.createProcess(cmdArgs)
	}
	defer func() {
		if p.pid == 0 && p.cancel != nil {
			p.cancel()
		}
	}()

	process.IsolateProcess(p.command)

	err := p.configureUser()
	if err != nil {
		return err
	}

	p.command.Env = p.setEnv(p.service.Env)
	// redirect stderr and stdout into the Write function of the process.go
	p.command.Stderr = p
	p.command.Stdout = p
	p.command.WaitDelay = time.Second * time.Duration(p.service.TimeoutStopSec) //nolint:gosec

	// non-blocking process start
	err = p.command.Start()
	if err != nil {
		return err
	}

	p.pid = int64(p.command.Process.Pid)

	// start process waiting routine
	go p.wait()

	return nil
}

// create command for the process with ExecTimeout
func (p *Process) createProcessCtx(cmdArgs []string) {
	if len(cmdArgs) < 2 {
		var ctx context.Context
		ctx, p.cancel = context.WithTimeout(context.Background(), p.service.ExecTimeout)
		p.command = exec.CommandContext(ctx, p.service.Command) //nolint:gosec
	} else {
		var ctx context.Context
		ctx, p.cancel = context.WithTimeout(context.Background(), p.service.ExecTimeout)
		p.command = exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...) //nolint:gosec
	}
}

// create command for the process
func (p *Process) createProcess(cmdArgs []string) {
	if len(cmdArgs) < 2 {
		p.command = exec.CommandContext(context.Background(), p.service.Command) //nolint:gosec
	} else {
		p.command = exec.CommandContext(context.Background(), cmdArgs[0], cmdArgs[1:]...) //nolint:gosec
	}
}

func (p *Process) configureUser() error {
	if p.service.User != "" {
		err := process.ExecuteFromUser(p.command, p.service.User)
		if err != nil {
			return err
		}
	}

	return nil
}

// wait completes before the execution's completion channel closes.
func (p *Process) wait() {
	defer close(p.done)
	err := p.command.Wait()
	if err != nil {
		p.log.Error("wait", "error", err)
	}

	if p.cancel != nil {
		p.cancel()
	}
	if p.onExit != nil {
		p.onExit(p)
	}
}

// stop waits for child completion, including forced termination.
func (p *Process) stop() {
	if p.command.Process == nil {
		return
	}
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.command.Process.Signal(syscall.SIGINT)
	timer := time.NewTimer(time.Second * time.Duration(p.service.TimeoutStopSec)) //nolint:gosec
	defer timer.Stop()
	select {
	case <-p.done:
	case <-timer.C:
		_ = p.command.Process.Kill()
		<-p.done
	}
}

func (p *Process) setEnv(e Env) []string {
	env := make([]string, 0, len(os.Environ())+len(e))
	env = append(env, os.Environ()...)
	for k, v := range e {
		env = append(env, strings.ToUpper(k)+"="+os.ExpandEnv(v))
	}
	return env
}
