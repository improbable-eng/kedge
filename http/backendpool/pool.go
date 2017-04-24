package backendpool

import (
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var (
	ErrUnknownBackend = grpc.Errorf(codes.Unimplemented, "unknown backend")
)

type Pool interface {
	// Tripper returns an already established http.RoundTripper just for this backend.
	Tripper(backendName string) (http.RoundTripper, error)
}
