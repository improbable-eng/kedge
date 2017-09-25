package backendpool

import (
	"fmt"

	"net/http"

	pb "github.com/mwitkow/kedge/_protogen/kedge/config/http/backends"
	"github.com/sirupsen/logrus"
)

// static is a Pool with a static configuration.
type static struct {
	backends map[string]*backend
}

// NewStatic creates a backend pool that has static configuration.
func NewStatic(backends []*pb.Backend) (*static, error) {
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

func (s *static) LogTestResolution(logger logrus.FieldLogger) {
	for k, backend := range s.backends {
		backend.LogTestResolution(logger.WithField("backend", k))
	}
}

func (s *static) Close() {
	for _, backend := range s.backends {
		backend.Close()
	}
}