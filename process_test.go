package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/process"
	"github.com/stretchr/testify/require"
)

func TestSetEnv(t *testing.T) {
	p := &Process{}
	out := p.setEnv(Env{"foo": "bar", "bar": "baz"})

	require.Len(t, out, len(os.Environ())+2)
	require.Subset(t, out, []string{"FOO=bar", "BAR=baz"})
}

func TestCreateProcess(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		execTimeout time.Duration
		args        []string
	}{
		{name: "single token", command: "sleep", args: []string{"sleep"}},
		{name: "command with arguments", command: "sleep 30", args: []string{"sleep", "30"}},
		{name: "single token with timeout", command: "sleep", execTimeout: time.Second, args: []string{"sleep"}},
		{name: "arguments with timeout", command: "sleep 30", execTimeout: time.Second, args: []string{"sleep", "30"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Process{service: &Service{Command: tt.command, ExecTimeout: tt.execTimeout}}
			if tt.execTimeout > 0 {
				p.createProcessCtx(strings.Split(tt.command, " "))
				t.Cleanup(p.cancel)
			} else {
				p.createProcess(strings.Split(tt.command, " "))
			}

			require.Equal(t, tt.args, p.command.Args)
		})
	}
}

func TestProcessWriteTrimsOutput(t *testing.T) {
	log, store := newCaptureLogger()
	p := &Process{log: log}

	payload := []byte("service output \n\t")
	n, err := p.Write(payload)

	require.NoError(t, err)
	require.Len(t, payload, n)
	require.Equal(t, []string{"service output"}, store.messages())
}

func TestNewServiceProcessDefaults(t *testing.T) {
	log, store := newCaptureLogger()

	svc := &Service{}
	p := NewServiceProcess(svc, "some_service", log)

	require.EqualValues(t, 30, p.service.RestartSec)
	require.EqualValues(t, 5, p.service.TimeoutStopSec)

	p.log.Info("output")
	rec, ok := store.find("output")
	require.True(t, ok)
	require.NotContains(t, rec.attrs, "service")
}

func TestNewServiceProcessServiceNameInLog(t *testing.T) {
	log, store := newCaptureLogger()

	svc := &Service{UseServiceName: true, RestartSec: 3, TimeoutStopSec: 7}
	p := NewServiceProcess(svc, "some_service", log)

	require.EqualValues(t, 3, p.service.RestartSec)
	require.EqualValues(t, 7, p.service.TimeoutStopSec)

	p.log.Info("output")
	rec, ok := store.find("output")
	require.True(t, ok)
	require.Equal(t, "some_service", rec.attrs["service"])
}

func TestProcessStopAfterFailedStart(t *testing.T) {
	tests := []struct {
		name    string
		command string
		user    string
	}{
		{name: "missing executable", command: filepath.Join(t.TempDir(), "no-such-binary")},
		{name: "user configuration failure", command: "sleep 30", user: "roadrunner-user-that-cannot-exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, _ := newCaptureLogger()
			p := NewServiceProcess(&Service{Command: tt.command, User: tt.user, TimeoutStopSec: 30}, testServiceName, log)
			require.Error(t, p.start())
			require.Zero(t, p.pid)

			started := time.Now()
			p.stop()
			require.Less(t, time.Since(started), time.Second)
		})
	}
}

func TestProcessStopKillsUnresponsiveChild(t *testing.T) {
	tests := []struct {
		name        string
		execTimeout time.Duration
	}{
		{name: "without exec timeout", execTimeout: 0},
		{name: "with exec timeout", execTimeout: time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := writeScript(t, "ignore-sigint.sh", "#!/bin/sh\ntrap '' INT\necho ready\nsleep 5\n")

			log, store := newCaptureLogger()
			p := NewServiceProcess(&Service{
				Command:        script,
				ExecTimeout:    tt.execTimeout,
				TimeoutStopSec: 1,
			}, "some_service", log)

			require.NoError(t, p.start())
			pid := p.pid
			require.NotZero(t, pid)
			t.Cleanup(p.stop)

			// the marker is written once the child ignores SIGINT
			require.Eventually(t, func() bool { return store.count("ready") == 1 },
				time.Second*10, time.Millisecond*20)

			started := time.Now()
			p.stop()

			// SIGINT is ignored, so stop falls through to the timer and kills the child
			require.GreaterOrEqual(t, time.Since(started), time.Second)
			require.Eventually(t, func() bool { return !processAlive(pid) },
				time.Second*10, time.Millisecond*20)
		})
	}
}

func TestProcessWaitBoundsInheritedPipes(t *testing.T) {
	tests := []struct {
		name        string
		execTimeout time.Duration
		stopSeconds uint64
		stop        bool
		naturalExit bool
		minimum     time.Duration
		limit       time.Duration
	}{
		{name: "stop without execution deadline", stopSeconds: 1, stop: true, minimum: 1900 * time.Millisecond, limit: 3 * time.Second},
		{name: "stop with execution deadline", execTimeout: time.Minute, stopSeconds: 1, stop: true, minimum: 1900 * time.Millisecond, limit: 3 * time.Second},
		{name: "two second stop budget", stopSeconds: 2, stop: true, minimum: 3900 * time.Millisecond, limit: 5 * time.Second},
		{name: "natural exit", stopSeconds: 1, naturalExit: true, minimum: 900 * time.Millisecond, limit: 2 * time.Second},
		{name: "execution deadline", execTimeout: time.Second, stopSeconds: 1, minimum: 1900 * time.Millisecond, limit: 3 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ending := "wait\n"
			if tt.naturalExit {
				ending = "exit 0\n"
			}
			script := writeScript(t, "retained-pipes.sh", "#!/bin/sh\ntrap '' INT\nsleep 30 &\necho $! > \"$0.pid\"\necho ready\n"+ending)
			log, store := newCaptureLogger()
			p := NewServiceProcess(&Service{Command: script, ExecTimeout: tt.execTimeout, TimeoutStopSec: tt.stopSeconds}, testServiceName, log)
			started := time.Now()
			require.NoError(t, p.start())
			require.NotZero(t, p.pid)
			t.Cleanup(p.stop)
			require.Eventually(t, func() bool { return store.count("ready") == 1 }, 5*time.Second, 10*time.Millisecond)
			data, err := os.ReadFile(script + ".pid")
			require.NoError(t, err)
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			require.NoError(t, err)
			require.Positive(t, pid)
			holder, err := os.FindProcess(pid)
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = holder.Kill()
				_ = holder.Release()
			})
			done := p.done
			if tt.stop {
				started = time.Now()
				done = make(chan struct{})
				go func() {
					p.stop()
					close(done)
				}()
			}
			select {
			case <-done:
			case <-time.After(tt.limit):
				t.Fatal("inherited output pipes blocked child completion beyond the execution budget")
			}
			require.GreaterOrEqual(t, time.Since(started), tt.minimum)
			require.False(t, processAlive(p.pid), "the direct child must be reaped")
			require.True(t, processAlive(int64(pid)), "the subprocess must still hold the inherited pipes")
		})
	}
}

func TestProcessRestartsAfterExit(t *testing.T) {
	script := writeScript(t, "exit-at-once.sh", "#!/bin/sh\necho ready\n")

	log, store := newCaptureLogger()
	g := newGroup(&Service{
		Command:         script,
		ProcessNum:      1,
		RemainAfterExit: true,
		RestartSec:      1,
		TimeoutStopSec:  1,
	}, "some_service", log)

	require.NoError(t, g.start())
	t.Cleanup(g.stop)

	// the child exits at once and is started again restart_sec later
	require.Eventually(t, func() bool { return store.count("ready") >= 2 },
		time.Second*15, time.Millisecond*20)
}

// writeScript drops an executable posix shell script into the test temp dir.
func writeScript(t *testing.T, name, body string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a posix shell script")
	}

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	require.NoError(t, os.Chmod(path, 0o700))

	return path
}

// processAlive reports whether the pid still refers to a live process.
func processAlive(pid int64) bool {
	alive, err := process.PidExists(int32(pid)) //nolint:gosec
	return err == nil && alive
}
