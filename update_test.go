package service

import (
	"math"
	"sync"
	"testing"
	"time"

	serviceV1 "github.com/roadrunner-server/api-go/v6/service/v1"
	"github.com/stretchr/testify/require"
)

func TestRPCUpdateValidation(t *testing.T) {
	tests := []struct {
		name  string
		patch *serviceV1.Update
	}{
		{name: "zero count", patch: &serviceV1.Update{ProcessNum: new(int64(0))}},
		{name: "negative count", patch: &serviceV1.Update{ProcessNum: new(int64(-1))}},
		{name: "negative timeout", patch: &serviceV1.Update{ExecTimeout: new(int64(-1))}},
		{name: "execution duration overflow", patch: &serviceV1.Update{ExecTimeout: new(int64(9223372037))}},
		{name: "stop duration overflow", patch: &serviceV1.Update{TimeoutStopSec: new(uint64(9223372037))}},
		{name: "restart duration overflow", patch: &serviceV1.Update{RestartSec: new(uint64(9223372037))}},
		{name: "unsigned stop overflow", patch: &serviceV1.Update{TimeoutStopSec: new(uint64(math.MaxUint64))}},
		{name: "unsigned restart overflow", patch: &serviceV1.Update{RestartSec: new(uint64(math.MaxUint64))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestRPC(t)
			desired := Service{
				Command:        "sleep 30",
				ProcessNum:     1,
				ExecTimeout:    20 * time.Second,
				RestartSec:     7,
				TimeoutStopSec: 3,
				Env:            Env{"VALUE": "accepted"},
			}
			g := newGroup(&desired, testServiceName, r.p.logger)
			r.p.processes.Store(testServiceName, g)
			tt.patch.Name = testServiceName
			if tt.patch.ProcessNum == nil {
				tt.patch.ProcessNum = new(int64(2))
			}
			tt.patch.Env = &serviceV1.Environment{Values: map[string]string{"VALUE": "rejected"}}
			tt.patch.ServiceNameInLogs = new(true)
			require.Error(t, r.Update(tt.patch, &serviceV1.Response{}))
			require.Equal(t, desired, g.desired)
		})
	}
}

func TestRPCUpdateOmissionAndCopies(t *testing.T) {
	r := newTestRPC(t)
	request := newCreate(testServiceName, 1)
	request.Env = map[string]string{"VALUE": "created"}
	request.ExecTimeout = 20
	require.NoError(t, r.Create(request, &serviceV1.Response{}))
	request.Env["VALUE"] = "changed-create-request"
	first := loadProcs(t, r)[0]
	patch := &serviceV1.Update{Name: testServiceName, Env: &serviceV1.Environment{Values: map[string]string{"VALUE": "updated"}}, TimeoutStopSec: new(uint64(4))}
	require.NoError(t, r.Update(patch, &serviceV1.Response{}))
	patch.Env.Values["VALUE"] = "changed-update-request"
	require.NoError(t, r.Update(&serviceV1.Update{Name: testServiceName, ProcessNum: new(int64(2)), RestartSec: new(uint64(7))}, &serviceV1.Response{}))
	require.NoError(t, r.Update(&serviceV1.Update{Name: testServiceName}, &serviceV1.Response{}))
	require.Equal(t, "created", first.service.Env["VALUE"])
	require.NoError(t, r.Restart(&serviceV1.Service{Name: testServiceName}, &serviceV1.Response{}))
	procs := loadProcs(t, r)
	require.Len(t, procs, 2)
	for _, proc := range procs {
		require.Contains(t, proc.command.Env, "VALUE=updated")
		require.Equal(t, 20*time.Second, proc.service.ExecTimeout)
		require.Equal(t, uint64(4), proc.service.TimeoutStopSec)
		require.Equal(t, uint64(7), proc.service.RestartSec)
	}
}

func TestRPCUpdateBoundsAndZero(t *testing.T) {
	tests := []struct {
		name                  string
		patch                 *serviceV1.Update
		wantStop, wantRestart uint64
		wantExec              time.Duration
	}{
		{name: "zero resets nondefault values", patch: &serviceV1.Update{TimeoutStopSec: new(uint64(0)), RestartSec: new(uint64(0)), ExecTimeout: new(int64(0))}, wantStop: 5, wantRestart: 30},
		{name: "largest whole second durations", patch: &serviceV1.Update{TimeoutStopSec: new(uint64(9223372036)), RestartSec: new(uint64(9223372036)), ExecTimeout: new(int64(9223372036))}, wantStop: 9223372036, wantRestart: 9223372036, wantExec: 9223372036000000000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestRPC(t)
			request := newCreate(testServiceName, 1)
			request.ExecTimeout = 20
			require.NoError(t, r.Create(request, &serviceV1.Response{}))
			tt.patch.Name = testServiceName
			require.NoError(t, r.Update(tt.patch, &serviceV1.Response{}))
			require.NoError(t, r.Restart(&serviceV1.Service{Name: testServiceName}, &serviceV1.Response{}))
			proc := loadProcs(t, r)[0]
			require.Equal(t, tt.wantStop, proc.service.TimeoutStopSec)
			require.Equal(t, tt.wantRestart, proc.service.RestartSec)
			require.Equal(t, tt.wantExec, proc.service.ExecTimeout)
		})
	}
}

func TestRPCUpdateUnknown(t *testing.T) {
	r := newTestRPC(t)
	require.ErrorIs(t, r.Update(&serviceV1.Update{Name: "missing"}, &serviceV1.Response{}), errNoSuchService)
}

func TestRPCCreateInvalidRuntimeValues(t *testing.T) {
	tests := []struct {
		name    string
		request *serviceV1.Create
	}{
		{name: "negative count", request: &serviceV1.Create{ProcessNum: -1}},
		{name: "negative execution", request: &serviceV1.Create{ProcessNum: 1, ExecTimeout: -1}},
		{name: "execution overflow", request: &serviceV1.Create{ProcessNum: 1, ExecTimeout: 9223372037}},
		{name: "restart overflow", request: &serviceV1.Create{ProcessNum: 1, RestartSec: math.MaxUint64}},
		{name: "stop overflow", request: &serviceV1.Create{ProcessNum: 1, TimeoutStopSec: math.MaxUint64}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestRPC(t)
			tt.request.Name, tt.request.Command = testServiceName, "sleep 30"
			require.Error(t, r.Create(tt.request, &serviceV1.Response{}))
		})
	}
}

func TestRPCUpdateConcurrentPartial(t *testing.T) {
	r := newTestRPC(t)
	require.NoError(t, r.Create(newCreate(testServiceName, 1), &serviceV1.Response{}))
	patches := []struct {
		name  string
		patch *serviceV1.Update
	}{
		{name: "count", patch: &serviceV1.Update{ProcessNum: new(int64(2))}},
		{name: "execution", patch: &serviceV1.Update{ExecTimeout: new(int64(20))}},
		{name: "stop", patch: &serviceV1.Update{TimeoutStopSec: new(uint64(3))}},
		{name: "restart", patch: &serviceV1.Update{RestartSec: new(uint64(7))}},
		{name: "logs", patch: &serviceV1.Update{ServiceNameInLogs: new(true)}},
		{name: "environment", patch: &serviceV1.Update{Env: &serviceV1.Environment{Values: map[string]string{"VALUE": "concurrent"}}}},
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, tt := range patches {
		wg.Go(func() {
			<-start
			tt.patch.Name = testServiceName
			caller := &rpc{p: r.p}
			err := caller.Update(tt.patch, &serviceV1.Response{})
			if err != nil {
				t.Errorf("%s: %v", tt.name, err)
			}
		})
	}
	close(start)
	wg.Wait()
	require.NoError(t, r.p.Reset())
	procs := loadProcs(t, r)
	require.Len(t, procs, 2)
	for _, proc := range procs {
		require.Equal(t, 20*time.Second, proc.service.ExecTimeout)
		require.Equal(t, uint64(3), proc.service.TimeoutStopSec)
		require.Equal(t, uint64(7), proc.service.RestartSec)
		require.True(t, proc.service.UseServiceName)
		require.Contains(t, proc.command.Env, "VALUE=concurrent")
	}
}

func TestRPCUpdateMaximumProcessCount(t *testing.T) {
	r := newTestRPC(t)
	require.NoError(t, r.Create(newCreate(testServiceName, 1), &serviceV1.Response{}))
	before := rpcPids(t, r)
	require.NoError(t, r.Update(&serviceV1.Update{Name: testServiceName, ProcessNum: new(int64(math.MaxInt))}, &serviceV1.Response{}))
	require.Equal(t, before, rpcPids(t, r))
	if int64(math.MaxInt) < math.MaxInt64 {
		require.Error(t, r.Update(&serviceV1.Update{Name: testServiceName, ProcessNum: new(int64(math.MaxInt64))}, &serviceV1.Response{}))
	}
}

func TestRPCUpdateRestartFailureRetainsExitStatus(t *testing.T) {
	r := newTestRPC(t)
	log, store := newCaptureLogger()
	r.p.logger = log
	request := newCreate(testServiceName, 1)
	request.Command = writeScript(t, "delete-on-exit.sh", "#!/bin/sh\necho ready\nrm -- \"$0\"\n")
	request.RemainAfterExit = true
	require.NoError(t, r.Create(request, &serviceV1.Response{}))
	before := rpcPids(t, r)[0]
	require.Eventually(t, func() bool { return store.count("process start error") == 1 }, 5*time.Second, 10*time.Millisecond)
	statuses := &serviceV1.Statuses{}
	require.NoError(t, r.Statuses(&serviceV1.Service{Name: testServiceName}, statuses))
	require.Len(t, statuses.GetStatus(), 1)
	require.EqualValues(t, before, statuses.GetStatus()[0].GetPid())
	require.NotEmpty(t, statuses.GetStatus()[0].GetStatus().GetMessage())
}
