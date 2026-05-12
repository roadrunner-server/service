package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	serviceProto "github.com/roadrunner-server/api-go/v6/service/v1"
	"github.com/roadrunner-server/api-go/v6/service/v1/serviceV1connect"
	"github.com/roadrunner-server/config/v6"
	"github.com/roadrunner-server/endure/v2"
	"github.com/roadrunner-server/logger/v6"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/service/v6"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const serviceAPIAddr = "127.0.0.1:6001"

// startServiceAPIContainer brings up rpc + service + logger on serviceAPIAddr
// with no services pre-configured. Returns a stop function the test must defer.
func startServiceAPIContainer(t *testing.T) func() {
	t.Helper()

	cont := endure.New(slog.LevelError)
	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-service-api.yaml",
	}

	require.NoError(t, cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&rpcPlugin.Plugin{},
		&service.Plugin{},
	))
	require.NoError(t, cont.Init())

	ch, err := cont.Serve()
	require.NoError(t, err)

	wg := &sync.WaitGroup{}
	stop := make(chan struct{})
	wg.Go(func() {
		select {
		case e := <-ch:
			t.Errorf("container reported error: %v", e.Error)
		case <-stop:
		}
	})

	time.Sleep(500 * time.Millisecond)

	return func() {
		close(stop)
		require.NoError(t, cont.Stop())
		wg.Wait()
	}
}

// TestServiceConnectAPI exercises the service RPCs through the Connect-RPC
// client (h2c). Cycle: CreateService → ListServices → GetStatuses →
// Terminate → ListServices.
func TestServiceConnectAPI(t *testing.T) {
	stop := startServiceAPIContainer(t)
	defer stop()

	httpc := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, network, addr)
			},
		},
	}
	client := serviceV1connect.NewServiceManagerClient(httpc, "http://"+serviceAPIAddr)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	const name = "connect-svc"

	createResp, err := client.CreateService(ctx, connect.NewRequest(&serviceProto.Create{
		Name:       name,
		Command:    "sleep 60",
		ProcessNum: 1,
	}))
	require.NoError(t, err)
	require.True(t, createResp.Msg.GetOk())

	// give the OS a moment so /proc/<pid> is populated for GetStatuses
	time.Sleep(200 * time.Millisecond)

	listResp, err := client.ListServices(ctx, connect.NewRequest(&serviceProto.Service{}))
	require.NoError(t, err)
	require.Contains(t, listResp.Msg.GetServices(), name)

	statusResp, err := client.GetStatuses(ctx, connect.NewRequest(&serviceProto.Service{Name: name}))
	require.NoError(t, err)
	require.Len(t, statusResp.Msg.GetStatus(), 1)
	require.Greater(t, statusResp.Msg.GetStatus()[0].GetPid(), int32(0))

	terminateResp, err := client.Terminate(ctx, connect.NewRequest(&serviceProto.Service{Name: name}))
	require.NoError(t, err)
	require.True(t, terminateResp.Msg.GetOk())

	listResp, err = client.ListServices(ctx, connect.NewRequest(&serviceProto.Service{}))
	require.NoError(t, err)
	require.NotContains(t, listResp.Msg.GetServices(), name)
}

// TestServiceHTTPApi exercises the service RPCs through plain HTTP/1.1 with
// a protojson body — the wire shape PHP clients use via Guzzle/curl
// (PHP has no Connect SDK).
func TestServiceHTTPApi(t *testing.T) {
	stop := startServiceAPIContainer(t)
	defer stop()

	httpc := &http.Client{Timeout: 30 * time.Second}
	ctx := t.Context()

	call := func(method string, in proto.Message, out proto.Message) {
		body, err := protojson.Marshal(in)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"http://"+serviceAPIAddr+"/service.v1.ServiceManager/"+method, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpc.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equalf(t, http.StatusOK, resp.StatusCode, "method=%s body=%s", method, respBody)
		require.NoError(t, protojson.Unmarshal(respBody, out))
	}

	const name = "http-svc"

	var createResp serviceProto.Response
	call("CreateService", &serviceProto.Create{
		Name:       name,
		Command:    "sleep 60",
		ProcessNum: 1,
	}, &createResp)
	require.True(t, createResp.GetOk())

	time.Sleep(200 * time.Millisecond)

	var listResp serviceProto.List
	call("ListServices", &serviceProto.Service{}, &listResp)
	require.Contains(t, listResp.GetServices(), name)

	var statusResp serviceProto.Statuses
	call("GetStatuses", &serviceProto.Service{Name: name}, &statusResp)
	require.Len(t, statusResp.GetStatus(), 1)
	require.Greater(t, statusResp.GetStatus()[0].GetPid(), int32(0))

	var terminateResp serviceProto.Response
	call("Terminate", &serviceProto.Service{Name: name}, &terminateResp)
	require.True(t, terminateResp.GetOk())

	var listResp2 serviceProto.List
	call("ListServices", &serviceProto.Service{}, &listResp2)
	require.NotContains(t, listResp2.GetServices(), name)
}

// TestServiceGRPCApi exercises the service RPCs through a regular gRPC
// client (google.golang.org/grpc). The same Connect handler serves gRPC
// framing off the same port — used by PHP's gRPC extension.
func TestServiceGRPCApi(t *testing.T) {
	stop := startServiceAPIContainer(t)
	defer stop()

	conn, err := grpc.NewClient(serviceAPIAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	client := serviceProto.NewServiceManagerClient(conn)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	const name = "grpc-svc"

	createResp, err := client.CreateService(ctx, &serviceProto.Create{
		Name:       name,
		Command:    "sleep 60",
		ProcessNum: 1,
	})
	require.NoError(t, err)
	require.True(t, createResp.GetOk())

	time.Sleep(200 * time.Millisecond)

	listResp, err := client.ListServices(ctx, &serviceProto.Service{})
	require.NoError(t, err)
	require.Contains(t, listResp.GetServices(), name)

	statusResp, err := client.GetStatuses(ctx, &serviceProto.Service{Name: name})
	require.NoError(t, err)
	require.Len(t, statusResp.GetStatus(), 1)
	require.Greater(t, statusResp.GetStatus()[0].GetPid(), int32(0))

	terminateResp, err := client.Terminate(ctx, &serviceProto.Service{Name: name})
	require.NoError(t, err)
	require.True(t, terminateResp.GetOk())

	listResp, err = client.ListServices(ctx, &serviceProto.Service{})
	require.NoError(t, err)
	require.NotContains(t, listResp.GetServices(), name)
}

// TestServiceHTTPGetIdempotency verifies which methods accept HTTP GET.
// Read-only methods (GetStatus, GetStatuses, ListServices) are marked
// `option idempotency_level = NO_SIDE_EFFECTS;` in the proto, so Connect
// accepts GET with the request encoded in query params. Mutating methods
// stay POST-only, so GET against them returns 405 Method Not Allowed.
func TestServiceHTTPGetIdempotency(t *testing.T) {
	stop := startServiceAPIContainer(t)
	defer stop()

	body, err := protojson.Marshal(&serviceProto.Service{Name: "probe"})
	require.NoError(t, err)

	q := url.Values{}
	q.Set("encoding", "json")
	q.Set("base64", "1")
	q.Set("message", base64.URLEncoding.EncodeToString(body))

	cases := []struct {
		method      string
		expectAllow bool
	}{
		{"GetStatus", true},
		{"GetStatuses", true},
		{"ListServices", true},
		{"CreateService", false},
		{"Terminate", false},
		{"Restart", false},
	}

	httpc := &http.Client{Timeout: 30 * time.Second}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
				"http://"+serviceAPIAddr+"/service.v1.ServiceManager/"+c.method+"?"+q.Encode(), nil)
			require.NoError(t, err)

			resp, err := httpc.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			respBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			if c.expectAllow {
				// Anything except 405 proves Connect routed the GET. The body may
				// be a NotFound for our synthetic "probe" service — that's still
				// a 4xx on the *application* layer, not the HTTP-method layer.
				require.NotEqualf(t, http.StatusMethodNotAllowed, resp.StatusCode,
					"%s via GET should be allowed; got 405\n%s", c.method, respBody)
				return
			}
			require.Equalf(t, http.StatusMethodNotAllowed, resp.StatusCode,
				"%s via GET should be rejected; got %s\n%s", c.method, resp.Status, respBody)
		})
	}
}
