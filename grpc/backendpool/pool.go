package backendpool

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var (
	ErrUnknownBackend = grpc.Errorf(codes.Unimplemented, "unknown backend")
)

type Pool interface {
	// Conn returns a dialled grpc.ClientConn for a given backend name.
	Conn(backendName string) (*grpc.ClientConn, error)

	// Close closes all the connections of the pool.
	Close() error
}
