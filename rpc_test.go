package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	serviceV1 "github.com/roadrunner-server/api-go/v6/service/v1"
	"github.com/stretchr/testify/require"
)

const (
	testServiceName  = "some_service"
	otherServiceName = "other_service"
)

func TestRPCCreateRejectsEmptyGroup(t *testing.T) {
	r := newTestRPC(t)

	out := &serviceV1.Response{}
	err := r.Create(&serviceV1.Create{Name: testServiceName, Command: "sleep 30"}, out)

	require.ErrorContains(t, err, "at least 1 process")
	require.False(t, out.GetOk())

	_, stored := r.p.processes.Load(testServiceName)
	require.False(t, stored)
}

func TestRPCCreateTwice(t *testing.T) {
	r := newTestRPC(t)

	in := &serviceV1.Create{
		Name:            testServiceName,
		Command:         "sleep 30",
		ProcessNum:      1,
		Env:             map[string]string{"foo": "bar"},
		RestartSec:      1,
		TimeoutStopSec:  1,
		RemainAfterExit: false,
	}
	require.NoError(t, r.Create(in, &serviceV1.Response{}))
	running := rpcPids(t, r)

	err := r.Create(in, &serviceV1.Response{})

	require.ErrorIs(t, err, errServiceExists)
	// the group that is already running is left alone
	require.Equal(t, running, rpcPids(t, r))
}

func TestRPCCreateBrokenCommand(t *testing.T) {
	r := newTestRPC(t)

	out := &serviceV1.Response{}
	err := r.Create(&serviceV1.Create{
		Name:       testServiceName,
		Command:    filepath.Join(t.TempDir(), "no-such-binary"),
		ProcessNum: 2,
	}, out)

	require.Error(t, err)
	require.False(t, out.GetOk())

	_, stored := r.p.processes.Load(testServiceName)
	require.False(t, stored)
}

func TestRPCUnknownService(t *testing.T) {
	r := newTestRPC(t)
	in := &serviceV1.Service{Name: testServiceName}

	require.ErrorIs(t, r.Terminate(in, &serviceV1.Response{}), errNoSuchService)
	require.ErrorIs(t, r.Restart(in, &serviceV1.Response{}), errNoSuchService)
	require.ErrorIs(t, r.Status(in, &serviceV1.Status{}), errNoSuchService)
	require.ErrorIs(t, r.Statuses(in, &serviceV1.Statuses{}), errNoSuchService)
}

func TestRPCRestartReplacesProcesses(t *testing.T) {
	r := newTestRPC(t)
	require.NoError(t, r.Create(newCreate(testServiceName, 2), &serviceV1.Response{}))

	before := rpcPids(t, r)
	require.Len(t, before, 2)

	out := &serviceV1.Response{}
	require.NoError(t, r.Restart(&serviceV1.Service{Name: testServiceName}, out))
	require.True(t, out.GetOk())

	after := rpcPids(t, r)
	require.Len(t, after, 2)
	for _, pid := range after {
		require.NotContains(t, before, pid)
	}

	for _, pid := range before {
		require.Eventually(t, func() bool { return !processAlive(pid) }, time.Second*10, time.Millisecond*20)
	}
}

func TestRPCRestartRollsBackBrokenReplacement(t *testing.T) {
	r := newTestRPC(t)
	log, store := newCaptureLogger()
	r.p.logger = log
	require.NoError(t, r.Create(newCreate(testServiceName, 2), &serviceV1.Response{}))

	procs := loadProcs(t, r)
	before := rpcPids(t, r)
	script := writeScript(t, "replacement.sh", "#!/bin/sh\necho replacement-ready\nexec sleep 30\n")
	handler := &replacementHandler{Handler: log.Handler(), ready: make(chan struct{}, 1)}
	handler.removeCommand = sync.OnceFunc(func() {
		select {
		case <-handler.ready:
			handler.err = os.Remove(script)
		case <-time.After(5 * time.Second):
			handler.err = errors.New("replacement did not report readiness")
		}
	})
	g, err := r.loadGroup(testServiceName)
	require.NoError(t, err)
	t.Cleanup(g.stop)
	g.mu.Lock()
	g.desired.Command = script
	g.log = slog.New(handler)
	g.mu.Unlock()

	require.Error(t, r.Restart(&serviceV1.Service{Name: testServiceName}, &serviceV1.Response{}))
	require.NoError(t, handler.err)
	require.Equal(t, 3, store.count("service was started"), "two original children and one replacement must start")
	replacement := g.snapshot()[0]
	require.NotZero(t, replacement.pid)
	require.NotContains(t, before, replacement.pid)
	require.False(t, processAlive(replacement.pid), "the partial replacement must be reaped")

	_, stored := r.p.processes.Load(testServiceName)
	require.False(t, stored)

	for i := range procs {
		require.Eventually(t, func() bool { return !processAlive(procs[i].pid) },
			time.Second*10, time.Millisecond*20)
	}
}

// replacementHandler removes the command after its first replacement reports readiness.
type replacementHandler struct {
	slog.Handler
	ready         chan struct{}
	removeCommand func()
	err           error
}

func (h *replacementHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "replacement-ready" {
		select {
		case h.ready <- struct{}{}:
		default:
		}
	}
	if record.Message == "service was started" {
		h.removeCommand()
	}
	return h.Handler.Handle(ctx, record)
}

func TestRPCTerminateStopsProcesses(t *testing.T) {
	r := newTestRPC(t)
	require.NoError(t, r.Create(newCreate(testServiceName, 2), &serviceV1.Response{}))
	procs := loadProcs(t, r)

	out := &serviceV1.Response{}
	require.NoError(t, r.Terminate(&serviceV1.Service{Name: testServiceName}, out))
	require.True(t, out.GetOk())

	_, stored := r.p.processes.Load(testServiceName)
	require.False(t, stored)

	for i := range procs {
		require.Eventually(t, func() bool { return !processAlive(procs[i].pid) },
			time.Second*10, time.Millisecond*20)
	}
}

func TestRPCListAndStatuses(t *testing.T) {
	r := newTestRPC(t)
	require.NoError(t, r.Create(newCreate(testServiceName, 2), &serviceV1.Response{}))
	require.NoError(t, r.Create(newCreate(otherServiceName, 1), &serviceV1.Response{}))

	list := &serviceV1.List{}
	require.NoError(t, r.List(&serviceV1.Service{}, list))
	require.ElementsMatch(t, []string{testServiceName, otherServiceName}, list.GetServices())

	statuses := &serviceV1.Statuses{}
	require.NoError(t, r.Statuses(&serviceV1.Service{Name: testServiceName}, statuses))
	require.Len(t, statuses.GetStatus(), 2)

	for _, st := range statuses.GetStatus() {
		require.Nil(t, st.GetStatus())
		require.NotZero(t, st.GetPid())
		require.NotZero(t, st.GetMemoryUsage())
		require.Contains(t, st.GetCommand(), "sleep")
	}

	// Status keeps only the last process of the group
	status := &serviceV1.Status{}
	require.NoError(t, r.Status(&serviceV1.Service{Name: testServiceName}, status))
	require.Equal(t, statuses.GetStatus()[1].GetPid(), status.GetPid())
	require.Equal(t, statuses.GetStatus()[1].GetCommand(), status.GetCommand())
}

func TestRPCStatusesReportsDeadProcess(t *testing.T) {
	r := newTestRPC(t)
	require.NoError(t, r.Create(newCreate(testServiceName, 1), &serviceV1.Response{}))

	procs := loadProcs(t, r)
	procs[0].stop()
	require.Eventually(t, func() bool { return !processAlive(procs[0].pid) },
		time.Second*10, time.Millisecond*20)

	statuses := &serviceV1.Statuses{}
	require.NoError(t, r.Statuses(&serviceV1.Service{Name: testServiceName}, statuses))
	require.Len(t, statuses.GetStatus(), 1)

	// the pid and the command are still reported, the state carries the error
	require.NotZero(t, statuses.GetStatus()[0].GetPid())
	require.Contains(t, statuses.GetStatus()[0].GetCommand(), "sleep")
	require.NotEmpty(t, statuses.GetStatus()[0].GetStatus().GetMessage())

	// Status has no per-process error slot and fails instead
	require.Error(t, r.Status(&serviceV1.Service{Name: testServiceName}, &serviceV1.Status{}))
}

// newTestRPC returns an rpc handler over a plugin wired to an in-memory logger,
// stopped at the end of the test.
func newTestRPC(t *testing.T) *rpc {
	t.Helper()

	log, _ := newCaptureLogger()
	p := &Plugin{logger: log}
	t.Cleanup(func() { _ = p.Stop(context.Background()) })

	return &rpc{p: p}
}

// newCreate builds a create request for a long-lived command.
func newCreate(name string, processNum int64) *serviceV1.Create {
	return &serviceV1.Create{
		Name:           name,
		Command:        "sleep 30",
		ProcessNum:     processNum,
		RestartSec:     1,
		TimeoutStopSec: 1,
	}
}

// loadProcs returns the processes stored under the test service name.
func loadProcs(t *testing.T, r *rpc) []*Process {
	t.Helper()

	v, ok := r.p.processes.Load(testServiceName)
	require.True(t, ok)

	return v.(*group).snapshot()
}

// rpcPids returns the pids of the processes stored under the test service name.
func rpcPids(t *testing.T, r *rpc) []int64 {
	t.Helper()

	procs := loadProcs(t, r)
	pids := make([]int64, 0, len(procs))
	for i := range procs {
		require.NotZero(t, procs[i].pid)
		pids = append(pids, procs[i].pid)
	}

	return pids
}
