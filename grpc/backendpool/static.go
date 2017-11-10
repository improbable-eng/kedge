package backendpool

import (
	"fmt"

	pb "github.com/improbable-eng/kedge/_protogen/kedge/config/grpc/backends"
	"google.golang.org/grpc"
)

// static is a Pool with a static configuration.
type static struct {
	backends map[string]*backend
}

func (s *static) Close() error {
	for _, be := range s.backends {
		be.Close()
	}
	return nil
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

func (s *static) Conn(backendName string) (*grpc.ClientConn, error) {
	be, ok := s.backends[backendName]
	if !ok {
		return nil, ErrUnknownBackend
	}
	return be.Conn()
}
