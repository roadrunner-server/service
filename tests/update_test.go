package tests

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"tests/helpers"

	serviceV1 "github.com/roadrunner-server/api-go/v6/service/v1"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/service/v6"
	"github.com/shirou/gopsutil/process"
	"github.com/stretchr/testify/require"
)

// TestServiceUpdateChild is the controlled subprocess for Update tests.
func TestServiceUpdateChild(t *testing.T) {
	if os.Getenv("RR_UPDATE_CHILD") != "1" {
		return
	}
	if os.Getenv("RR_UPDATE_IGNORE_INT") == "1" {
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, syscall.SIGINT)
		defer signal.Stop(interrupts)
		go func() {
			select {
			case <-interrupts:
				fmt.Printf("update-interrupt-%d\n", os.Getpid())
			case <-t.Context().Done():
			}
		}()
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", os.Getenv("RR_UPDATE_CONTROL"))
	require.NoError(t, err)
	defer conn.Close()
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	data, err := json.Marshal(childReady{PID: int32(os.Getpid()), Env: env})
	require.NoError(t, err)
	_, err = fmt.Fprintln(conn, string(data))
	require.NoError(t, err)
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		switch scanner.Text() {
		case "exit":
			return
		case "log":
			fmt.Printf("update-output-%d\n", os.Getpid())
		}
	}
}

type childReady struct {
	PID int32
	Env map[string]string
}

type updateChild struct {
	childReady
	conn net.Conn
	done chan struct{}
}

func (c *updateChild) send(t *testing.T, command string) {
	t.Helper()
	_, err := fmt.Fprintln(c.conn, command)
	require.NoError(t, err)
}

func (c *updateChild) exited(t *testing.T) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(10 * time.Second):
		t.Fatal("child did not exit")
	}
}

func (c *updateChild) reaped(t *testing.T) {
	t.Helper()
	alive, err := process.PidExists(c.PID)
	require.NoError(t, err)
	require.False(t, alive, "child remains after termination returned")
}

type updateFixture struct {
	ready   chan *updateChild
	command string
}

func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Setenv("RR_UPDATE_CONTROL", listener.Addr().String())
	t.Setenv("RR_UPDATE_CHILD", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("RR_UPDATE_INHERITED", "parent")
	t.Setenv("RR_UPDATE_SHADOW", "parent-shadow")
	executable, err := os.Executable()
	require.NoError(t, err)
	f := &updateFixture{ready: make(chan *updateChild, 64), command: executable + " -test.run=^TestServiceUpdateChild$"}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer conn.Close()
				stop := context.AfterFunc(t.Context(), func() { _ = conn.Close() })
				defer stop()
				_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				reader := bufio.NewReader(conn)
				data, err := reader.ReadBytes('\n')
				if err != nil {
					return
				}
				child := &updateChild{conn: conn, done: make(chan struct{})}
				if json.Unmarshal(data, &child.childReady) != nil {
					return
				}
				_ = conn.SetReadDeadline(time.Time{})
				f.ready <- child
				_, _ = io.Copy(io.Discard, reader)
				close(child.done)
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		wg.Wait()
	})
	return f
}

func (f *updateFixture) next(t *testing.T) *updateChild {
	t.Helper()
	select {
	case child := <-f.ready:
		return child
	case <-time.After(10 * time.Second):
		t.Fatal("next execution did not report readiness")
		return nil
	}
}

func (f *updateFixture) quiet(t *testing.T, duration time.Duration) {
	t.Helper()
	select {
	case child := <-f.ready:
		t.Fatalf("unexpected execution: %d", child.PID)
	case <-time.After(duration):
	}
}

func startUpdateRPC(t *testing.T) (*helpers.RR, *rpc.Client, *service.Plugin, func()) {
	t.Helper()
	p := &service.Plugin{}
	rr, stop := helpers.Start(t, "configs/.rr-service-create-empty.yaml",
		[]any{p, &rpcPlugin.Plugin{}}, helpers.WithTCPProbe("127.0.0.1:6311"))
	return rr, helpers.RPC(t, "127.0.0.1:6311"), p, stop
}

func TestServiceRPCUpdateParameters(t *testing.T) {
	tests := []struct {
		name    string
		initial *serviceV1.Create
		patch   *serviceV1.Update
		check   func(*testing.T, *updateFixture, *helpers.RR, *rpc.Client, []*updateChild)
	}{
		{
			name: "count increase", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: true},
			patch: &serviceV1.Update{ProcessNum: new(int64(3))},
			check: func(t *testing.T, f *updateFixture, _ *helpers.RR, c *rpc.Client, before []*updateChild) {
				before[0].send(t, "exit")
				a, b, d := f.next(t), f.next(t), f.next(t)
				require.ElementsMatch(t, []int32{a.PID, b.PID, d.PID}, statusPids(t, helpers.Statuses(t, c, "update")))
				f.quiet(t, 1200*time.Millisecond)
			},
		},
		{
			name: "count decrease", initial: &serviceV1.Create{ProcessNum: 3, RemainAfterExit: true},
			patch: &serviceV1.Update{ProcessNum: new(int64(1))},
			check: func(t *testing.T, f *updateFixture, _ *helpers.RR, c *rpc.Client, before []*updateChild) {
				before[0].send(t, "exit")
				before[1].send(t, "exit")
				before[0].exited(t)
				before[1].exited(t)
				f.quiet(t, 1200*time.Millisecond)
				require.Contains(t, statusPids(t, helpers.Statuses(t, c, "update")), before[2].PID)
				before[2].send(t, "exit")
				after := f.next(t)
				require.NotEqual(t, before[2].PID, after.PID)
				f.quiet(t, 1200*time.Millisecond)
			},
		},
		{
			name: "stop timeout", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: true, TimeoutStopSec: 2},
			patch: &serviceV1.Update{TimeoutStopSec: new(uint64(1))},
			check: func(t *testing.T, f *updateFixture, _ *helpers.RR, c *rpc.Client, before []*updateChild) {
				start := time.Now()
				helpers.Restart(t, c, "update")
				require.GreaterOrEqual(t, time.Since(start), 2*time.Second)
				before[0].exited(t)
				before[0].reaped(t)
				after := f.next(t)
				start = time.Now()
				helpers.Terminate(t, c, "update")
				require.GreaterOrEqual(t, time.Since(start), time.Second)
				require.Less(t, time.Since(start), 2*time.Second)
				after.exited(t)
				after.reaped(t)
			},
		},
		{
			name: "finite execution timeout", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: true},
			patch: &serviceV1.Update{ExecTimeout: new(int64(1))},
			check: func(t *testing.T, f *updateFixture, _ *helpers.RR, c *rpc.Client, before []*updateChild) {
				f.quiet(t, 1300*time.Millisecond)
				require.Equal(t, []int32{before[0].PID}, statusPids(t, helpers.Statuses(t, c, "update")))
				before[0].send(t, "exit")
				after := f.next(t)
				after.exited(t)
				require.NotEqual(t, after.PID, f.next(t).PID)
			},
		},
		{
			name: "explicit unlimited execution", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: true, ExecTimeout: 2},
			patch: &serviceV1.Update{ExecTimeout: new(int64(0))},
			check: func(t *testing.T, f *updateFixture, _ *helpers.RR, c *rpc.Client, before []*updateChild) {
				before[0].exited(t)
				after := f.next(t)
				f.quiet(t, 2300*time.Millisecond)
				require.Equal(t, []int32{after.PID}, statusPids(t, helpers.Statuses(t, c, "update")))
				after.send(t, "log")
			},
		},
		{
			name: "disable restart", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: true},
			patch: &serviceV1.Update{RemainAfterExit: new(false)},
			check: func(t *testing.T, f *updateFixture, _ *helpers.RR, _ *rpc.Client, before []*updateChild) {
				before[0].send(t, "exit")
				before[0].exited(t)
				f.quiet(t, 1300*time.Millisecond)
			},
		},
		{
			name: "enable restart", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: false},
			patch: &serviceV1.Update{RemainAfterExit: new(true)},
			check: func(t *testing.T, f *updateFixture, _ *helpers.RR, _ *rpc.Client, before []*updateChild) {
				before[0].send(t, "exit")
				require.NotEqual(t, before[0].PID, f.next(t).PID)
			},
		},
		{
			name: "disable log attribute", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: true, ServiceNameInLogs: true},
			patch: &serviceV1.Update{ServiceNameInLogs: new(false)},
			check: checkUpdateLogs(true, false),
		},
		{
			name: "enable log attribute", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: true},
			patch: &serviceV1.Update{ServiceNameInLogs: new(true)},
			check: checkUpdateLogs(false, true),
		},
		{
			name: "environment replacement", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: true, Env: map[string]string{"RR_UPDATE_REMOVED": "old", "RR_UPDATE_SHADOW": "old-shadow"}},
			patch: &serviceV1.Update{Env: &serviceV1.Environment{Values: map[string]string{"rr_update_empty": "", "rr_update_zero": "0", "rr_update_expanded": "${RR_UPDATE_INHERITED}"}}},
			check: func(t *testing.T, f *updateFixture, _ *helpers.RR, _ *rpc.Client, before []*updateChild) {
				require.Equal(t, "old", before[0].Env["RR_UPDATE_REMOVED"])
				before[0].send(t, "exit")
				env := f.next(t).Env
				require.NotContains(t, env, "RR_UPDATE_REMOVED")
				require.Equal(t, "parent-shadow", env["RR_UPDATE_SHADOW"])
				require.Equal(t, "parent", env["RR_UPDATE_INHERITED"])
				require.Contains(t, env, "RR_UPDATE_EMPTY")
				require.Empty(t, env["RR_UPDATE_EMPTY"])
				require.Equal(t, "0", env["RR_UPDATE_ZERO"])
				require.Equal(t, "parent", env["RR_UPDATE_EXPANDED"])
			},
		},
		{
			name: "environment clearing", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: true, Env: map[string]string{"RR_UPDATE_REMOVED": "old", "RR_UPDATE_SHADOW": "old-shadow"}},
			patch: &serviceV1.Update{Env: &serviceV1.Environment{Values: map[string]string{}}},
			check: func(t *testing.T, f *updateFixture, _ *helpers.RR, _ *rpc.Client, before []*updateChild) {
				before[0].send(t, "exit")
				env := f.next(t).Env
				require.NotContains(t, env, "RR_UPDATE_REMOVED")
				require.Equal(t, "parent", env["RR_UPDATE_INHERITED"])
				require.Equal(t, "parent-shadow", env["RR_UPDATE_SHADOW"])
			},
		},
		{
			name: "restart delay", initial: &serviceV1.Create{ProcessNum: 1, RemainAfterExit: true, RestartSec: 30},
			patch: &serviceV1.Update{RestartSec: new(uint64(1))},
			check: func(t *testing.T, f *updateFixture, _ *helpers.RR, _ *rpc.Client, before []*updateChild) {
				start := time.Now()
				before[0].send(t, "exit")
				f.next(t)
				require.GreaterOrEqual(t, time.Since(start), time.Second)
				require.Less(t, time.Since(start), 5*time.Second)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newUpdateFixture(t)
			if tt.name == "stop timeout" {
				t.Setenv("RR_UPDATE_IGNORE_INT", "1")
			}
			rr, client, _, _ := startUpdateRPC(t)
			tt.initial.Name, tt.initial.Command = "update", f.command
			if tt.initial.RestartSec == 0 {
				tt.initial.RestartSec = 1
			}
			helpers.Create(t, client, tt.initial)
			before := make([]*updateChild, 0, tt.initial.ProcessNum)
			for range tt.initial.ProcessNum {
				before = append(before, f.next(t))
			}
			pids := statusPids(t, helpers.Statuses(t, client, "update"))
			tt.patch.Name = "update"
			helpers.Update(t, client, tt.patch)
			require.ElementsMatch(t, pids, statusPids(t, helpers.Statuses(t, client, "update")))
			for _, child := range before {
				select {
				case <-child.done:
					t.Fatal("Update stopped a current execution")
				default:
				}
			}
			tt.check(t, f, rr, client, before)
		})
	}
}

func checkUpdateLogs(oldValue, newValue bool) func(*testing.T, *updateFixture, *helpers.RR, *rpc.Client, []*updateChild) {
	return func(t *testing.T, f *updateFixture, rr *helpers.RR, _ *rpc.Client, before []*updateChild) {
		assertLog := func(child *updateChild, want bool) {
			child.send(t, "log")
			message := fmt.Sprintf("update-output-%d", child.PID)
			rr.WaitLogsExact(t, message, 1)
			records := rr.Logs.FilterMessage(message).All()
			if want {
				require.Equal(t, "update", records[0].Attrs["service"])
			} else {
				require.NotContains(t, records[0].Attrs, "service")
			}
		}
		assertLog(before[0], oldValue)
		before[0].send(t, "exit")
		assertLog(f.next(t), newValue)
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
	slices.Sort(pids)
	return pids
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
			f := newUpdateFixture(t)
			rr, client, _, stop := startUpdateRPC(t)
			helpers.Create(t, client, &serviceV1.Create{Name: "update", Command: f.command, ProcessNum: 1, RemainAfterExit: true, RestartSec: 3})
			before := f.next(t)
			start := time.Now()
			before.send(t, "exit")
			before.exited(t)
			rr.WaitLogsExact(t, "service restart scheduled", 1)
			require.Eventually(t, func() bool { return len(liveUpdatePIDs(t, client)) == 0 }, 5*time.Second, 10*time.Millisecond)
			if tt.patch != nil {
				tt.patch.Name = "update"
				helpers.Update(t, client, tt.patch)
				if tt.name == "disable cancels queued start" {
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
				f.quiet(t, 3300*time.Millisecond)
				return
			}
			after := f.next(t)
			require.GreaterOrEqual(t, time.Since(start), 3*time.Second)
			for range tt.wantCount - 1 {
				f.next(t)
			}
			require.Len(t, liveUpdatePIDs(t, client), tt.wantCount)
			if tt.name == "armed delay retains duration" {
				start = time.Now()
				after.send(t, "exit")
				f.next(t)
				require.GreaterOrEqual(t, time.Since(start), time.Second)
				require.Less(t, time.Since(start), 2500*time.Millisecond)
			}
		})
	}
}

func TestServiceRPCUpdateInactive(t *testing.T) {
	f := newUpdateFixture(t)
	_, client, _, _ := startUpdateRPC(t)
	helpers.Create(t, client, &serviceV1.Create{Name: "update", Command: f.command, ProcessNum: 1, RestartSec: 1})
	before := f.next(t)
	before.send(t, "exit")
	before.exited(t)
	require.Eventually(t, func() bool { return len(liveUpdatePIDs(t, client)) == 0 }, 5*time.Second, 10*time.Millisecond)
	helpers.Update(t, client, &serviceV1.Update{Name: "update", RemainAfterExit: new(true), ProcessNum: new(int64(2))})
	f.quiet(t, 1300*time.Millisecond)
	helpers.Restart(t, client, "update")
	f.next(t)
	f.next(t)
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
			f := newUpdateFixture(t)
			p := &service.Plugin{}
			config := "version: '3'\nrpc:\n  listen: tcp://127.0.0.1:6311\nservice: {}\n"
			if tt.yaml {
				config = fmt.Sprintf("version: '3'\nrpc:\n  listen: tcp://127.0.0.1:6311\nservice:\n  update:\n    command: %q\n    process_num: 1\n    service_name_in_log: true\n    restart_sec: 1\n    env:\n      VALUE: old\n", f.command)
			}
			rr, _ := helpers.Start(t, "", []any{p, &rpcPlugin.Plugin{}}, helpers.WithInlineConfig(config), helpers.WithTCPProbe("127.0.0.1:6311"))
			client := helpers.RPC(t, "127.0.0.1:6311")
			if !tt.yaml {
				helpers.Create(t, client, &serviceV1.Create{Name: "update", Command: f.command, ProcessNum: 1, ServiceNameInLogs: true, RestartSec: 1, Env: map[string]string{"VALUE": "old"}})
			}
			before := f.next(t)
			before.send(t, "log")
			msg := fmt.Sprintf("update-output-%d", before.PID)
			rr.WaitLogsExact(t, msg, 1)
			require.Equal(t, "update", rr.Logs.FilterMessage(msg).All()[0].Attrs["service"])
			helpers.Update(t, client, &serviceV1.Update{Name: "update", ProcessNum: new(int64(2)), Env: &serviceV1.Environment{Values: map[string]string{"VALUE": "new"}}})
			if tt.reset {
				require.NoError(t, p.Reset())
			} else {
				helpers.Restart(t, client, "update")
			}
			before.exited(t)
			for range 2 {
				after := f.next(t)
				require.Equal(t, "new", after.Env["VALUE"])
				require.NotEqual(t, before.PID, after.PID)
			}
			require.Len(t, liveUpdatePIDs(t, client), 2)
		})
	}
}

func TestServiceRPCUpdateConcurrentLifecycle(t *testing.T) {
	f := newUpdateFixture(t)
	_, client, p, _ := startUpdateRPC(t)
	helpers.Create(t, client, &serviceV1.Create{Name: "update", Command: f.command, ProcessNum: 4, RemainAfterExit: true, RestartSec: 1})
	children := []*updateChild{f.next(t), f.next(t), f.next(t), f.next(t)}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			<-start
			out := &serviceV1.Response{}
			err := client.Call("service.Update", &serviceV1.Update{Name: "update", ProcessNum: new(int64(2)), Env: &serviceV1.Environment{Values: map[string]string{"VALUE": "latest"}}}, out)
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
		for _, child := range children {
			_, _ = fmt.Fprintln(child.conn, "exit")
		}
	})
	wg.Go(func() {
		<-start
		if err := client.Call("service.Terminate", &serviceV1.Service{Name: "update"}, &serviceV1.Response{}); err != nil {
			t.Errorf("Terminate: %v", err)
		}
	})
	close(start)
	wg.Wait()
	for _, child := range children {
		child.exited(t)
		child.reaped(t)
	}
	require.Empty(t, helpers.List(t, client))
	f.quiet(t, 1300*time.Millisecond)
	helpers.Create(t, client, &serviceV1.Create{Name: "update", Command: f.command, ProcessNum: 1})
	f.next(t)
	f.quiet(t, 1300*time.Millisecond)
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
			f := newUpdateFixture(t)
			rr, client, _, _ := startUpdateRPC(t)
			helpers.Create(t, client, &serviceV1.Create{Name: "update", Command: f.command, ProcessNum: 3, RemainAfterExit: true, RestartSec: 1})
			before := []*updateChild{f.next(t), f.next(t), f.next(t)}
			if !tt.queued {
				helpers.Update(t, client, &serviceV1.Update{Name: "update", ProcessNum: new(tt.target)})
			}
			var wg sync.WaitGroup
			for _, child := range before {
				wg.Go(func() { _, _ = fmt.Fprintln(child.conn, "exit") })
			}
			wg.Wait()
			require.Eventually(t, func() bool { return len(liveUpdatePIDs(t, client)) == 0 }, 5*time.Second, 10*time.Millisecond)
			if tt.queued {
				rr.WaitLogsExact(t, "service restart scheduled", 3)
				helpers.Update(t, client, &serviceV1.Update{Name: "update", ProcessNum: new(tt.target)})
			}
			for range tt.target {
				f.next(t)
			}
			require.Len(t, liveUpdatePIDs(t, client), int(tt.target))
			f.quiet(t, 1300*time.Millisecond)
		})
	}
}

func TestServiceRPCUpdateDuringRestart(t *testing.T) {
	f := newUpdateFixture(t)
	t.Setenv("RR_UPDATE_IGNORE_INT", "1")
	rr, client, _, _ := startUpdateRPC(t)
	helpers.Create(t, client, &serviceV1.Create{Name: "update", Command: f.command, ProcessNum: 1, TimeoutStopSec: 2})
	before := f.next(t)
	call := client.Go("service.Restart", &serviceV1.Service{Name: "update"}, &serviceV1.Response{}, make(chan *rpc.Call, 1))
	rr.WaitLogsExact(t, fmt.Sprintf("update-interrupt-%d", before.PID), 1)
	// A successful Update during the stop budget must reach the next execution.
	helpers.Update(t, client, &serviceV1.Update{Name: "update", ProcessNum: new(int64(2)), TimeoutStopSec: new(uint64(1)), Env: &serviceV1.Environment{Values: map[string]string{"VALUE": "during-stop"}}})
	select {
	case <-before.done:
		t.Fatal("Update waited for the current execution to stop")
	default:
	}
	select {
	case result := <-call.Done:
		require.NoError(t, result.Error)
	case <-time.After(5 * time.Second):
		t.Fatal("Restart did not finish")
	}
	before.reaped(t)
	for range 2 {
		require.Equal(t, "during-stop", f.next(t).Env["VALUE"])
	}
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
			f := newUpdateFixture(t)
			rr, client, p, _ := startUpdateRPC(t)
			helpers.Create(t, client, &serviceV1.Create{Name: "update", Command: f.command, ProcessNum: 1, RemainAfterExit: true, RestartSec: 1})
			before := f.next(t)
			before.send(t, "exit")
			rr.WaitLogsExact(t, "service restart scheduled", 1)
			helpers.Update(t, client, &serviceV1.Update{Name: "update", ProcessNum: new(int64(2)), Env: &serviceV1.Environment{Values: map[string]string{"VALUE": "replacement"}}})
			if tt.reset {
				require.NoError(t, p.Reset())
			} else {
				helpers.Restart(t, client, "update")
			}
			for range 2 {
				require.Equal(t, "replacement", f.next(t).Env["VALUE"])
			}
			f.quiet(t, 1300*time.Millisecond)
			require.Len(t, liveUpdatePIDs(t, client), 2)
		})
	}
}
