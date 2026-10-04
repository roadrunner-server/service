package tests

import (
	"encoding/json/v2"
	"net/rpc"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"tests/helpers"

	serviceV1 "github.com/roadrunner-server/api-go/v6/service/v1"
	"github.com/roadrunner-server/resetter/v6"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/service/v6"
	"github.com/shirou/gopsutil/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceRPCUpdateProcessCount(t *testing.T) {
	tests := []struct {
		name    string
		initial int64
		target  int64
	}{
		{name: "increase", initial: 1, target: 3},
		{name: "decrease", initial: 3, target: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
				[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
			client := helpers.RPC(t, rpcAddress)
			helpers.Create(t, client, &serviceV1.Create{
				Name: "update", Command: "php php_test_files/loop.php",
				ProcessNum: tt.initial, RemainAfterExit: true, RestartSec: 1,
			})
			rr.WaitLogs(t, "The number is: 0", int(tt.initial))
			before := statusPids(t, helpers.Statuses(t, client, "update"))

			helpers.Update(t, client, &serviceV1.Update{Name: "update", ProcessNum: new(tt.target)})
			require.ElementsMatch(t, before, liveUpdatePIDs(t, client))
			for _, pid := range before[:len(before)-1] {
				require.NoError(t, syscall.Kill(int(pid), syscall.SIGTERM))
			}
			require.Eventually(t, func() bool { return len(liveUpdatePIDs(t, client)) == 1 }, 5*time.Second, 20*time.Millisecond)
			require.Never(t, func() bool { return rr.Count("service was started") > int(tt.initial) }, 1200*time.Millisecond, 20*time.Millisecond)

			require.NoError(t, syscall.Kill(int(before[len(before)-1]), syscall.SIGTERM))
			rr.WaitLogs(t, "The number is: 0", int(tt.initial+tt.target))
			require.Len(t, liveUpdatePIDs(t, client), int(tt.target))
		})
	}
}

func TestServiceRPCUpdateExecTimeout(t *testing.T) {
	tests := []struct {
		name    string
		initial int64
		target  int64
	}{
		{name: "finite execution timeout", initial: 0, target: 1},
		{name: "explicit unlimited execution", initial: 2, target: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
				[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
			client := helpers.RPC(t, rpcAddress)
			helpers.Create(t, client, &serviceV1.Create{
				Name: "update", Command: "php php_test_files/loop.php",
				ProcessNum: 1, RemainAfterExit: true, RestartSec: 1, ExecTimeout: tt.initial,
			})
			rr.WaitLogs(t, "The number is: 0", 1)
			before := statusPids(t, helpers.Statuses(t, client, "update"))

			helpers.Update(t, client, &serviceV1.Update{Name: "update", ExecTimeout: new(tt.target)})
			require.Equal(t, before, liveUpdatePIDs(t, client))
			if tt.initial == 0 {
				require.Never(t, func() bool { return rr.CountExact("wait") > 0 }, 1300*time.Millisecond, 20*time.Millisecond)
				require.NoError(t, syscall.Kill(int(before[0]), syscall.SIGTERM))
			}
			rr.WaitLogs(t, "The number is: 0", 2)
			if tt.target == 0 {
				require.Never(t, func() bool { return rr.CountExact("wait") > 1 }, 2300*time.Millisecond, 20*time.Millisecond)
			} else {
				rr.WaitLogs(t, "The number is: 0", 3)
			}
		})
	}
}

func TestServiceRPCUpdateStopTimeout(t *testing.T) {
	rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
	client := helpers.RPC(t, rpcAddress)
	helpers.Create(t, client, &serviceV1.Create{
		Name: "update", Command: "php php_test_files/ignore_interrupt.php", ProcessNum: 1, TimeoutStopSec: 2,
	})
	rr.WaitLogs(t, "ready", 1)
	before := statusPids(t, helpers.Statuses(t, client, "update"))

	helpers.Update(t, client, &serviceV1.Update{Name: "update", TimeoutStopSec: new(uint64(1))})
	require.Equal(t, before, liveUpdatePIDs(t, client))
	start := time.Now()
	helpers.Restart(t, client, "update")
	require.GreaterOrEqual(t, time.Since(start), 2*time.Second)

	rr.WaitLogs(t, "ready", 2)
	after := statusPids(t, helpers.Statuses(t, client, "update"))
	start = time.Now()
	helpers.Terminate(t, client, "update")
	require.GreaterOrEqual(t, time.Since(start), time.Second)
	require.Less(t, time.Since(start), 2*time.Second)
	alive, err := process.PidExists(after[0])
	require.NoError(t, err)
	require.False(t, alive)
}

func TestServiceRPCUpdateAutomaticRestart(t *testing.T) {
	tests := []struct {
		name    string
		initial bool
		target  bool
	}{
		{name: "disable restart", initial: true, target: false},
		{name: "enable restart", initial: false, target: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
				[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
			client := helpers.RPC(t, rpcAddress)
			helpers.Create(t, client, &serviceV1.Create{
				Name: "update", Command: "php php_test_files/loop.php",
				ProcessNum: 1, RemainAfterExit: tt.initial, RestartSec: 1,
			})
			rr.WaitLogs(t, "The number is: 0", 1)
			before := statusPids(t, helpers.Statuses(t, client, "update"))

			helpers.Update(t, client, &serviceV1.Update{Name: "update", RemainAfterExit: new(tt.target)})
			require.Equal(t, before, liveUpdatePIDs(t, client))
			require.NoError(t, syscall.Kill(int(before[0]), syscall.SIGTERM))
			rr.WaitLogsExact(t, "wait", 1)
			if tt.target {
				rr.WaitLogs(t, "The number is: 0", 2)
			} else {
				require.Never(t, func() bool { return rr.Count("service was started") > 1 }, 1300*time.Millisecond, 20*time.Millisecond)
			}
		})
	}
}

func TestServiceRPCUpdateLogAttribute(t *testing.T) {
	tests := []struct {
		name    string
		initial bool
		target  bool
	}{
		{name: "disable attribute", initial: true, target: false},
		{name: "enable attribute", initial: false, target: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
				[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
			client := helpers.RPC(t, rpcAddress)
			helpers.Create(t, client, &serviceV1.Create{
				Name: "update", Command: "php php_test_files/loop.php",
				ProcessNum: 1, RemainAfterExit: true, RestartSec: 1, ServiceNameInLogs: tt.initial,
			})
			rr.WaitLogs(t, "The number is: 0", 1)
			before := statusPids(t, helpers.Statuses(t, client, "update"))

			helpers.Update(t, client, &serviceV1.Update{Name: "update", ServiceNameInLogs: new(tt.target)})
			require.Equal(t, before, liveUpdatePIDs(t, client))
			rr.WaitLogs(t, "The number is: 1", 1)
			current := rr.Logs.FilterMessage("The number is: 1").All()[0]
			if tt.initial {
				require.Equal(t, "update", current.Attrs["service"])
			} else {
				require.NotContains(t, current.Attrs, "service")
			}

			require.NoError(t, syscall.Kill(int(before[0]), syscall.SIGTERM))
			rr.WaitLogs(t, "The number is: 0", 2)
			next := rr.Logs.FilterMessage("The number is: 0").All()[1]
			if tt.target {
				require.Equal(t, "update", next.Attrs["service"])
			} else {
				require.NotContains(t, next.Attrs, "service")
			}
		})
	}
}

func TestServiceRPCUpdateEnvironment(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want map[string]string
	}{
		{
			name: "replace overrides",
			env:  map[string]string{"rr_update_empty": "", "rr_update_zero": "0", "rr_update_expanded": "${RR_UPDATE_INHERITED}"},
			want: map[string]string{"RR_UPDATE_EMPTY": "", "RR_UPDATE_ZERO": "0", "RR_UPDATE_EXPANDED": "parent", "RR_UPDATE_INHERITED": "parent", "RR_UPDATE_SHADOW": "parent-shadow"},
		},
		{
			name: "clear overrides",
			env:  map[string]string{},
			want: map[string]string{"RR_UPDATE_INHERITED": "parent", "RR_UPDATE_SHADOW": "parent-shadow"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("RR_UPDATE_INHERITED", "parent")
			t.Setenv("RR_UPDATE_SHADOW", "parent-shadow")
			rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
				[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
			client := helpers.RPC(t, rpcAddress)
			helpers.Create(t, client, &serviceV1.Create{
				Name: "update", Command: "php php_test_files/update_env.php",
				ProcessNum: 1, RemainAfterExit: true, RestartSec: 1,
				Env: map[string]string{"RR_UPDATE_REMOVED": "old", "RR_UPDATE_SHADOW": "old-shadow"},
			})
			rr.WaitLogs(t, `"RR_UPDATE_REMOVED":"old"`, 1)
			before := statusPids(t, helpers.Statuses(t, client, "update"))

			helpers.Update(t, client, &serviceV1.Update{Name: "update", Env: &serviceV1.Environment{Values: tt.env}})
			require.Equal(t, before, liveUpdatePIDs(t, client))
			require.NoError(t, syscall.Kill(int(before[0]), syscall.SIGTERM))
			rr.WaitLogs(t, `"RR_UPDATE_INHERITED"`, 2)
			var env map[string]string
			records := rr.Logs.FilterMessageSnippet(`"RR_UPDATE_INHERITED"`).All()
			require.NoError(t, json.Unmarshal([]byte(records[1].Message), &env))
			require.NotContains(t, env, "RR_UPDATE_REMOVED")
			require.Subset(t, env, tt.want)
		})
	}
}

func TestServiceRPCUpdateRestartDelay(t *testing.T) {
	rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
	client := helpers.RPC(t, rpcAddress)
	helpers.Create(t, client, &serviceV1.Create{
		Name: "update", Command: "php php_test_files/loop.php", ProcessNum: 1, RemainAfterExit: true, RestartSec: 30,
	})
	rr.WaitLogs(t, "The number is: 0", 1)
	before := statusPids(t, helpers.Statuses(t, client, "update"))

	helpers.Update(t, client, &serviceV1.Update{Name: "update", RestartSec: new(uint64(1))})
	require.Equal(t, before, liveUpdatePIDs(t, client))
	start := time.Now()
	require.NoError(t, syscall.Kill(int(before[0]), syscall.SIGTERM))
	rr.WaitLogs(t, "The number is: 0", 2)
	require.GreaterOrEqual(t, time.Since(start), time.Second)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestServiceRPCUpdateQueuedStarts(t *testing.T) {
	tests := []struct {
		name      string
		patch     *serviceV1.Update
		terminate bool
		stop      bool
		wantCount int
	}{
		{name: "armed delay retains duration", patch: &serviceV1.Update{RestartSec: new(uint64(1))}, wantCount: 1},
		{name: "queued opportunity increases target", patch: &serviceV1.Update{ProcessNum: new(int64(3))}, wantCount: 3},
		{name: "disable cancels queued start", patch: &serviceV1.Update{RemainAfterExit: new(false)}},
		{name: "terminate cancels queued start", terminate: true},
		{name: "stop cancels queued start", stop: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr, stop := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
				[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
			client := helpers.RPC(t, rpcAddress)
			helpers.Create(t, client, &serviceV1.Create{
				Name: "update", Command: "php php_test_files/loop.php", ProcessNum: 1, RemainAfterExit: true, RestartSec: 3,
			})
			rr.WaitLogs(t, "The number is: 0", 1)
			before := statusPids(t, helpers.Statuses(t, client, "update"))
			start := time.Now()
			require.NoError(t, syscall.Kill(int(before[0]), syscall.SIGTERM))
			rr.WaitLogsExact(t, "service restart scheduled", 1)

			if tt.patch != nil {
				tt.patch.Name = "update"
				helpers.Update(t, client, tt.patch)
				if tt.patch.RemainAfterExit != nil {
					helpers.Update(t, client, &serviceV1.Update{Name: "update", RemainAfterExit: new(true)})
				}
			}
			if tt.terminate {
				helpers.Terminate(t, client, "update")
			}
			if tt.stop {
				stop()
			}
			if tt.wantCount == 0 {
				require.Never(t, func() bool { return rr.Count("service was started") > 1 }, 3300*time.Millisecond, 20*time.Millisecond)
				return
			}
			rr.WaitLogs(t, "The number is: 0", 1+tt.wantCount)
			require.GreaterOrEqual(t, time.Since(start), 3*time.Second)
			after := liveUpdatePIDs(t, client)
			require.Len(t, after, tt.wantCount)
			if tt.patch.RestartSec != nil {
				start = time.Now()
				require.NoError(t, syscall.Kill(int(after[0]), syscall.SIGTERM))
				rr.WaitLogs(t, "The number is: 0", 2+tt.wantCount)
				require.GreaterOrEqual(t, time.Since(start), time.Second)
				require.Less(t, time.Since(start), 2500*time.Millisecond)
			}
		})
	}
}

func TestServiceRPCUpdateInactive(t *testing.T) {
	rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
	client := helpers.RPC(t, rpcAddress)
	helpers.Create(t, client, &serviceV1.Create{
		Name: "update", Command: "php php_test_files/loop.php", ProcessNum: 1, RestartSec: 1,
	})
	rr.WaitLogs(t, "The number is: 0", 1)
	before := statusPids(t, helpers.Statuses(t, client, "update"))
	require.NoError(t, syscall.Kill(int(before[0]), syscall.SIGTERM))
	require.Eventually(t, func() bool { return len(liveUpdatePIDs(t, client)) == 0 }, 5*time.Second, 20*time.Millisecond)

	helpers.Update(t, client, &serviceV1.Update{Name: "update", RemainAfterExit: new(true), ProcessNum: new(int64(2))})
	require.Never(t, func() bool { return rr.Count("service was started") > 1 }, 1300*time.Millisecond, 20*time.Millisecond)
	helpers.Restart(t, client, "update")
	rr.WaitLogs(t, "The number is: 0", 3)
	require.Len(t, liveUpdatePIDs(t, client), 2)
}

func TestServiceRPCUpdateRestartResetParity(t *testing.T) {
	tests := []struct {
		name  string
		yaml  bool
		reset bool
	}{
		{name: "RPC Restart"},
		{name: "RPC Reset", reset: true},
		{name: "YAML Restart", yaml: true},
		{name: "YAML Reset", yaml: true, reset: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := "version: '3'\nrpc:\n  listen: tcp://" + rpcAddress + "\nservice:"
			if tt.yaml {
				config += `
  update:
    command: php php_test_files/update_env.php
    process_num: 1
    env:
      VALUE: old
`
			} else {
				config += " {}\n"
			}
			rr, _ := helpers.Start(t, "", []any{&service.Plugin{}, &rpcPlugin.Plugin{}, &resetter.Plugin{}},
				helpers.WithInlineConfig(config), helpers.WithTCPProbe(rpcAddress))
			client := helpers.RPC(t, rpcAddress)
			if !tt.yaml {
				helpers.Create(t, client, &serviceV1.Create{
					Name: "update", Command: "php php_test_files/update_env.php", ProcessNum: 1, Env: map[string]string{"VALUE": "old"},
				})
			}
			rr.WaitLogs(t, `"VALUE":"old"`, 1)

			helpers.Update(t, client, &serviceV1.Update{
				Name: "update", ProcessNum: new(int64(2)), Env: &serviceV1.Environment{Values: map[string]string{"VALUE": "new"}},
			})
			if tt.reset {
				helpers.Reset(t, client, "service")
			} else {
				helpers.Restart(t, client, "update")
			}
			rr.WaitLogs(t, `"VALUE":"new"`, 2)
			require.Len(t, liveUpdatePIDs(t, client), 2)
		})
	}
}

func TestServiceRPCUpdateConcurrentLifecycle(t *testing.T) {
	p := &service.Plugin{}
	rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{p, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
	client := helpers.RPC(t, rpcAddress)
	helpers.Create(t, client, &serviceV1.Create{
		Name: "update", Command: "php php_test_files/loop.php", ProcessNum: 4, RemainAfterExit: true, RestartSec: 1,
	})
	rr.WaitLogs(t, "The number is: 0", 4)
	before := statusPids(t, helpers.Statuses(t, client, "update"))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			<-start
			err := client.Call("service.Update", &serviceV1.Update{
				Name: "update", ProcessNum: new(int64(2)), Env: &serviceV1.Environment{Values: map[string]string{"VALUE": "latest"}},
			}, &serviceV1.Response{})
			if err != nil && !strings.Contains(err.Error(), "no such service") {
				t.Errorf("Update: %v", err)
			}
		})
	}
	wg.Go(func() {
		<-start
		for range 10 {
			_ = p.Workers()
			_ = client.Call("service.Statuses", &serviceV1.Service{Name: "update"}, &serviceV1.Statuses{})
		}
	})
	wg.Go(func() {
		<-start
		for _, pid := range before {
			_ = syscall.Kill(int(pid), syscall.SIGTERM)
		}
	})
	wg.Go(func() {
		<-start
		assert.NoError(t, client.Call("service.Terminate", &serviceV1.Service{Name: "update"}, &serviceV1.Response{}))
	})
	close(start)
	wg.Wait()
	for _, pid := range before {
		alive, err := process.PidExists(pid)
		require.NoError(t, err)
		require.False(t, alive)
	}
	require.Empty(t, helpers.List(t, client))
	started := rr.Count("service was started")
	require.Never(t, func() bool { return rr.Count("service was started") > started }, 1300*time.Millisecond, 20*time.Millisecond)
}

func TestServiceRPCUpdateOverlappingExits(t *testing.T) {
	tests := []struct {
		name   string
		target int64
		queued bool
	}{
		{name: "increase with overlapping exits", target: 4},
		{name: "decrease with queued starts", target: 1, queued: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
				[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
			client := helpers.RPC(t, rpcAddress)
			helpers.Create(t, client, &serviceV1.Create{
				Name: "update", Command: "php php_test_files/loop.php", ProcessNum: 3, RemainAfterExit: true, RestartSec: 1,
			})
			rr.WaitLogs(t, "The number is: 0", 3)
			before := statusPids(t, helpers.Statuses(t, client, "update"))
			if !tt.queued {
				helpers.Update(t, client, &serviceV1.Update{Name: "update", ProcessNum: new(tt.target)})
			}
			var wg sync.WaitGroup
			for _, pid := range before {
				wg.Go(func() { assert.NoError(t, syscall.Kill(int(pid), syscall.SIGTERM)) })
			}
			wg.Wait()
			if tt.queued {
				rr.WaitLogsExact(t, "service restart scheduled", 3)
				helpers.Update(t, client, &serviceV1.Update{Name: "update", ProcessNum: new(tt.target)})
			}
			rr.WaitLogs(t, "The number is: 0", 3+int(tt.target))
			require.Len(t, liveUpdatePIDs(t, client), int(tt.target))
			require.Never(t, func() bool { return rr.Count("service was started") > 3+int(tt.target) }, 1300*time.Millisecond, 20*time.Millisecond)
		})
	}
}

func TestServiceRPCUpdateDuringRestart(t *testing.T) {
	rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{&service.Plugin{}, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
	client := helpers.RPC(t, rpcAddress)
	helpers.Create(t, client, &serviceV1.Create{
		Name: "update", Command: "php php_test_files/ignore_interrupt.php", ProcessNum: 1, TimeoutStopSec: 2,
	})
	rr.WaitLogs(t, "ready", 1)
	pid := statusPids(t, helpers.Statuses(t, client, "update"))[0]
	call := client.Go("service.Restart", &serviceV1.Service{Name: "update"}, &serviceV1.Response{}, make(chan *rpc.Call, 1))
	rr.WaitLogsExact(t, "interrupt", 1)

	helpers.Update(t, client, &serviceV1.Update{
		Name: "update", ProcessNum: new(int64(2)), TimeoutStopSec: new(uint64(1)),
		Env: &serviceV1.Environment{Values: map[string]string{"VALUE": "during-stop"}},
	})
	alive, err := process.PidExists(pid)
	require.NoError(t, err)
	require.True(t, alive, "Update waited for the current execution to stop")
	select {
	case result := <-call.Done:
		require.NoError(t, result.Error)
	case <-time.After(5 * time.Second):
		t.Fatal("Restart did not finish")
	}
	rr.WaitLogs(t, "ready VALUE=during-stop", 2)
	require.Len(t, liveUpdatePIDs(t, client), 2)
}

func TestServiceRPCUpdatePendingReplacement(t *testing.T) {
	tests := []struct {
		name  string
		reset bool
	}{
		{name: "Restart cancels old timer"},
		{name: "Reset cancels old timer", reset: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr, _ := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
				[]any{&service.Plugin{}, &rpcPlugin.Plugin{}, &resetter.Plugin{}}, helpers.WithTCPProbe(rpcAddress))
			client := helpers.RPC(t, rpcAddress)
			helpers.Create(t, client, &serviceV1.Create{
				Name: "update", Command: "php php_test_files/update_env.php", ProcessNum: 1,
				RemainAfterExit: true, RestartSec: 1, Env: map[string]string{"VALUE": "old"},
			})
			rr.WaitLogs(t, `"VALUE":"old"`, 1)
			before := statusPids(t, helpers.Statuses(t, client, "update"))
			require.NoError(t, syscall.Kill(int(before[0]), syscall.SIGTERM))
			rr.WaitLogsExact(t, "service restart scheduled", 1)

			helpers.Update(t, client, &serviceV1.Update{
				Name: "update", ProcessNum: new(int64(2)), Env: &serviceV1.Environment{Values: map[string]string{"VALUE": "replacement"}},
			})
			if tt.reset {
				helpers.Reset(t, client, "service")
			} else {
				helpers.Restart(t, client, "update")
			}
			rr.WaitLogs(t, `"VALUE":"replacement"`, 2)
			require.Never(t, func() bool { return rr.Count("service was started") > 3 }, 1300*time.Millisecond, 20*time.Millisecond)
		})
	}
}

func liveUpdatePIDs(t *testing.T, c *rpc.Client) []int32 {
	t.Helper()
	var pids []int32
	for _, status := range helpers.Statuses(t, c, "update") {
		if status.GetStatus() == nil {
			pids = append(pids, status.GetPid())
		}
	}
	return pids
}
