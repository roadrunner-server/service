package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		name    string
		command string
		args    []string
	}{
		{name: "single token", command: "sleep", args: []string{"sleep"}},
		{name: "command with arguments", command: "sleep 30", args: []string{"sleep", "30"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Process{service: &Service{Command: tt.command}}
			p.createProcess(strings.Split(tt.command, " "))

			require.Equal(t, tt.args, p.command.Args)
			require.Equal(t, "sleep", filepath.Base(p.command.Path))
			require.Nil(t, p.cancel)
		})
	}
}

func TestCreateProcessCtx(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
	}{
		{name: "single token", command: "sleep", args: []string{"sleep"}},
		{name: "command with arguments", command: "sleep 30", args: []string{"sleep", "30"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Process{service: &Service{Command: tt.command, ExecTimeout: time.Second}}
			p.createProcessCtx(strings.Split(tt.command, " "))
			t.Cleanup(p.cancel)

			require.Equal(t, tt.args, p.command.Args)
			require.Equal(t, "sleep", filepath.Base(p.command.Path))
			require.NotNil(t, p.cancel)
		})
	}
}

func TestConfigureUser(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		p := &Process{service: &Service{}}
		require.NoError(t, p.configureUser())
	})

	t.Run("unknown user", func(t *testing.T) {
		p := &Process{
			service: &Service{User: "roadrunner-user-that-cannot-exist"},
			command: exec.CommandContext(t.Context(), "sleep", "30"),
		}

		require.Error(t, p.configureUser())
	})
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

	require.EqualValues(t, 30, svc.RestartSec)
	require.EqualValues(t, 5, svc.TimeoutStopSec)

	p.log.Info("output")
	rec, ok := store.find("output")
	require.True(t, ok)
	require.NotContains(t, rec.attrs, "service")
}

func TestNewServiceProcessServiceNameInLog(t *testing.T) {
	log, store := newCaptureLogger()

	svc := &Service{UseServiceName: true, RestartSec: 3, TimeoutStopSec: 7}
	p := NewServiceProcess(svc, "some_service", log)

	require.EqualValues(t, 3, svc.RestartSec)
	require.EqualValues(t, 7, svc.TimeoutStopSec)

	p.log.Info("output")
	rec, ok := store.find("output")
	require.True(t, ok)
	require.Equal(t, "some_service", rec.attrs["service"])
}

func TestProcessStopWithoutStart(t *testing.T) {
	log, _ := newCaptureLogger()
	p := NewServiceProcess(&Service{Command: "sleep 30", TimeoutStopSec: 30}, "some_service", log)

	started := time.Now()
	p.stop()

	// there is no child to signal, so stop returns instead of arming the kill timer
	require.Less(t, time.Since(started), time.Second)
}

func TestProcessStartConfigureUserError(t *testing.T) {
	log, _ := newCaptureLogger()
	p := NewServiceProcess(&Service{Command: "sleep 30", User: "roadrunner-user-that-cannot-exist"}, "some_service", log)

	require.Error(t, p.start())
	require.Zero(t, p.pid)
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

func TestProcessRestartsAfterExit(t *testing.T) {
	script := writeScript(t, "exit-at-once.sh", "#!/bin/sh\necho ready\n")

	log, store := newCaptureLogger()
	p := NewServiceProcess(&Service{
		Command:         script,
		RemainAfterExit: true,
		RestartSec:      1,
		TimeoutStopSec:  1,
	}, "some_service", log)

	require.NoError(t, p.start())
	t.Cleanup(p.stop)

	// the child exits at once and is started again restart_sec later
	require.Eventually(t, func() bool { return store.count("ready") >= 2 },
		time.Second*15, time.Millisecond*20)
}

func TestProcessRestartError(t *testing.T) {
	script := writeScript(t, "delete-itself.sh", "#!/bin/sh\necho ready\nrm -- \"$0\"\n")

	log, store := newCaptureLogger()
	p := NewServiceProcess(&Service{
		Command:         script,
		RemainAfterExit: true,
		RestartSec:      1,
		TimeoutStopSec:  1,
	}, "some_service", log)

	require.NoError(t, p.start())
	t.Cleanup(p.stop)

	// the child removes its own command, so the restart cannot execute it
	require.Eventually(t, func() bool { return store.count("process start error") == 1 },
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
