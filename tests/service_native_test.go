package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	serviceProto "github.com/roadrunner-server/api-go/v6/service/v1"
	"github.com/roadrunner-server/api-go/v6/service/v1/serviceV1connect"
	"github.com/stretchr/testify/require"
)

// fakeServiceManager stubs only CreateService — the procedure the PHP worker
// `create_3_services.php` would have invoked via spiral/goridge. Every other
// method falls through to UnimplementedServiceManagerHandler, which returns
// CodeUnimplemented.
type fakeServiceManager struct {
	serviceV1connect.UnimplementedServiceManagerHandler
}

func (fakeServiceManager) CreateService(
	_ context.Context, _ *connect.Request[serviceProto.Create],
) (*connect.Response[serviceProto.Response], error) {
	return connect.NewResponse(&serviceProto.Response{Ok: true}), nil
}

// TestServiceNativeCreate is a pure request/response Connect-RPC smoke test
// for service.Manager.Create, mirroring what the (still-broken) PHP
// TestServicePHPCreate exercises. No Roadrunner container, no PHP — just
// proves the proto types + connectrpc wire round-trip for this procedure.
func TestServiceNativeCreate(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle(serviceV1connect.NewServiceManagerHandler(fakeServiceManager{}))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := serviceV1connect.NewServiceManagerClient(srv.Client(), strings.TrimSuffix(srv.URL, "/"))
	resp, err := client.CreateService(t.Context(), connect.NewRequest(&serviceProto.Create{
		Name:       "listen-jobs",
		Command:    "sleep 1",
		ProcessNum: 1,
	}))
	require.NoError(t, err)
	require.True(t, resp.Msg.GetOk())
}
