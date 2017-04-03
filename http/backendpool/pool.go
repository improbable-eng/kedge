package backendpool

import (
	"fmt"

	"net/http"

	pb "github.com/mwitkow/kedge/_protogen/kedge/config/http/backends"
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

// static is a Pool with a static configuration.
type static struct {
	backends map[string]*backend
}

// NewStatic creates a backend pool that has static configuration.
func NewStatic(backends []*pb.Backend) (Pool, error) {
	s := &static{backends: make(map[string]*backend)}
	for _, beCnf := range backends {
		be, err := newBackend(beCnf)
		if err != nil {
			return nil, fmt.Errorf("failed creating backend '%v': %v", beCnf.Name, err)
		}
		s.backends[beCnf.Name] = be
	}
	return s, nil
}

func (s *static) Tripper(backendName string) (http.RoundTripper, error) {
	be, ok := s.backends[backendName]
	if !ok {
		return nil, ErrUnknownBackend
	}
	return be.Tripper(), nil
}
