package lbtransport_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"io"

	"github.com/mwitkow/go-srvlb/grpc"
	"github.com/mwitkow/go-srvlb/srv"
	"github.com/mwitkow/kedge/http/lbtransport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

var (
	backendCount = 5
)

type BalancedTransportSuite struct {
	suite.Suite

	lbTrans http.RoundTripper

	backendHandler http.HandlerFunc
	backends       []*httptest.Server
	mu             sync.RWMutex // for overwriting the backendHandler
}

func TestBalancedTransportSuite(t *testing.T) {
	suite.Run(t, new(BalancedTransportSuite))
}

// implementation of the srv.Resolver against the suite's backends.
func (s *BalancedTransportSuite) Lookup(domainName string) ([]*srv.Target, error) {
	targets := []*srv.Target{}
	for _, b := range s.backends {
		t := &srv.Target{DialAddr: b.Listener.Addr().String(), Ttl: 1 * time.Second}
		targets = append(targets, t)
	}
	return targets, nil
}

func (s *BalancedTransportSuite) SetupSuite() {
	var err error
	// add `backendCount` backends to which we will be sending requests.
	funcForBackend := func(id int) http.HandlerFunc {
		return func(wrt http.ResponseWriter, req *http.Request) {
			s.mu.RLock()
			req.Header.Add("X-TEST-BACKEND-ID", fmt.Sprintf("%d", id))
			s.backendHandler(wrt, req)
			s.mu.RUnlock()
		}
	}
	for i := 0; i < backendCount; i++ {
		s.backends = append(s.backends, httptest.NewServer(funcForBackend(i)))
	}
	time.Sleep(500 * time.Millisecond) // wait for the backends to come up.
	s.lbTrans, err = lbtransport.New(
		"my-magic-srv",
		http.DefaultTransport,
		grpcsrvlb.New(s), // self implements resolver.Lookup
		lbtransport.RoundRobinPolicy())
	require.NoError(s.T(), err, "cannot fail on initialization")
}

func (s *BalancedTransportSuite) SetupTest() {
	// reset the backendHandler
	s.setBackendHandler(func(resp http.ResponseWriter, req *http.Request) {
		resp.WriteHeader(200)
	})
}

func (s *BalancedTransportSuite) setBackendHandler(handler http.HandlerFunc) {
	s.mu.Lock()
	s.backendHandler = handler
	s.mu.Unlock()
}

func (s *BalancedTransportSuite) TearDownSuite() {
	for _, b := range s.backends {
		b.Close()
	}
	s.lbTrans.(io.Closer).Close()
}

func (s *BalancedTransportSuite) TestRoundRobinSendsRequestsToAllBackends() {
	backendMapMu := sync.RWMutex{}
	backendMap := make(map[string]int)
	s.setBackendHandler(func(resp http.ResponseWriter, req *http.Request) {
		backendMapMu.Lock()
		backendId := req.Header.Get("X-TEST-BACKEND-ID")
		if _, ok := backendMap[backendId]; ok {
			backendMap[backendId] += 1
		} else {
			backendMap[backendId] = 1
		}
		backendMapMu.Unlock()
	})

	wg := sync.WaitGroup{}
	client := &http.Client{Transport: s.lbTrans, Timeout: 1 * time.Second}
	requestsPerBackend := 50
	for i := 0; i < requestsPerBackend*backendCount; i++ {
		wg.Add(1)
		go func(id int) {
			_, err := client.Get("http://my-magic-srv/something")
			if err != nil {
				s.T().Logf("Encountered error on request %v: %v", id, err)
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
	backendMapMu.RLock()
	defer backendMapMu.RUnlock()
	assert.Equal(s.T(), backendCount, len(backendMap), "srvlb should round robin across all backends")
	for k, val := range backendMap {
		assert.Equal(s.T(), requestsPerBackend, val, "backend %v is expected to have its share of requests", k)
	}
}

//func (s *BalancedTransportSuite) TestSrvLbErrorsOnBadTarget() {
//	client := &http.Client{Transport: s.lbTrans, Timeout: 1 * time.Second}
//	_, err := client.Get("http://not-my-magic-srv/something")
//	require.Error(s.T(), err, "srvlb should not be able to dial targets thOat are not known")
//}
