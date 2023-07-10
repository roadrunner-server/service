package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/roadrunner-server/sdk/v4/utils"
	"go.uber.org/zap"
)

// Process structure contains an information about process, restart information, log, errors, etc
type Process struct {
	sync.Mutex
	// command to execute
	command *exec.Cmd
	pid     int64

	// logger
	log     *zap.Logger
	service *Service
	cancel  context.CancelFunc

	// process start time
	stopped  uint64
	sigintCh chan struct{}
}

// NewServiceProcess constructs service process structure
func NewServiceProcess(service *Service, name string, l *zap.Logger) *Process {
	log := new(zap.Logger)
	*log = *l
	if service.UseServiceName {
		log = log.Named(name)
	}

	return &Process{
		service:  service,
		log:      log,
		sigintCh: make(chan struct{}, 1),
	}
}

// write message to the log (stderr)
func (p *Process) Write(b []byte) (int, error) {
	p.log.Info(string(bytes.TrimRight(bytes.TrimRight(bytes.TrimSpace(b), "\n"), "\t")))
	return len(b), nil
}

func (p *Process) start() error {
	p.Lock()
	defer p.Unlock()

	// cmdArgs contain command arguments if the command in form of: php <command> or ls <command> -i -b
	var cmdArgs []string
	cmdArgs = append(cmdArgs, strings.Split(p.service.Command, " ")...)

	// crate fat-process here
	if p.service.ExecTimeout > 0 {
		p.createProcessCtx(cmdArgs)
	} else {
		p.createProcess(cmdArgs)
	}

	utils.IsolateProcess(p.command)

	err := p.configureUser()
	if err != nil {
		return err
	}

	p.command.Env = p.setEnv(p.service.Env)
	// redirect stderr and stdout into the Write function of the process.go
	p.command.Stderr = p
	p.command.Stdout = p

	// non-blocking process start
	err = p.command.Start()
	if err != nil {
		return err
	}

	// save start time
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
		p.command = exec.Command(p.service.Command) //nolint:gosec
	} else {
		p.command = exec.Command(cmdArgs[0], cmdArgs[1:]...) //nolint:gosec
	}
}

func (p *Process) configureUser() error {
	if p.service.User != "" {
		err := utils.ExecuteFromUser(p.command, p.service.User)
		if err != nil {
			return err
		}
	}

	return nil
}

// wait process for exit
func (p *Process) wait() {
	// Wait error doesn't matter here
	err := p.command.Wait()
	if err != nil {
		p.log.Error("wait", zap.Error(err))
	}

	// select is optional here
	select {
	case p.sigintCh <- struct{}{}:
	default:
		break
	}

	// wait for restart delay
	if p.service.RemainAfterExit {
		if atomic.LoadUint64(&p.stopped) > 0 {
			return
		}
		// wait for the delay
		time.Sleep(time.Second * time.Duration(p.service.RestartSec))
		// and start command again
		err = p.start()
		if err != nil {
			p.log.Error("process start error", zap.Error(err))
			return
		}
	}
}

// stop can be only sent by endure when plugin stopped
func (p *Process) stop() {
	atomic.StoreUint64(&p.stopped, 1)
	p.Lock()
	defer p.Unlock()

	if p.cancel != nil {
		p.cancel()
	}

	if p.command == nil || p.command.Process == nil {
		return
	}

	_ = p.command.Process.Signal(syscall.SIGINT)

	ta := time.NewTimer(time.Second * 5)
	select {
	case <-ta.C:
		_ = p.command.Process.Signal(syscall.SIGKILL)
		ta.Stop()

		select {
		case <-p.sigintCh:
		default:
			break
		}
	case <-p.sigintCh:
		ta.Stop()
		return
	}
}

func (p *Process) setEnv(e Env) []string {
	env := make([]string, 0, len(os.Environ())+len(e))
	env = append(env, os.Environ()...)
	for k, v := range e {
		env = append(env, fmt.Sprintf("%s=%s", strings.ToUpper(k), os.Expand(v, os.Getenv)))
	}
	return env
}
