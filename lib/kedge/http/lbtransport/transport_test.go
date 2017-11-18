package lbtransport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/improbable-eng/kedge/lib/reporter"
	"github.com/improbable-eng/kedge/lib/reporter/errtypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc/naming"
	"io/ioutil"
)

var (
	testBackendCount = 5
	testDialTimeout  = 500 * time.Millisecond
)

// BalancedRRTransportSuite test lbtransport with round robin + blacklist load balancing.
type BalancedRRTransportSuite struct {
	suite.Suite

	policy  *roundRobinPolicy
	ctx context.Context
	cancelFn context.CancelFunc

	lbTrans *tripper

	backendHandler   http.HandlerFunc
	backendHandlerMu sync.RWMutex

	backendSRVWatcher *mockSRVWatcher
	backends          []*httptest.Server
}

func TestRRBalancedTransportSuite(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)()

	suite.Run(t, new(BalancedRRTransportSuite))
}

// implementation of the naming.Resolver returning mocked watcher.
func (s *BalancedRRTransportSuite) Resolve(_ string) (naming.Watcher, error) {
	return s.backendSRVWatcher, nil
}

// mockedHandler mocks backend behaviour, which fills request header with proper backend number.
func (s *BalancedRRTransportSuite) mockedHandler(id int) http.HandlerFunc {
	return func(wrt http.ResponseWriter, req *http.Request) {
		s.backendHandlerMu.RLock()
		req.Header.Add("X-TEST-BACKEND-ID", fmt.Sprintf("%d", id))
		s.backendHandler(wrt, req)
		s.backendHandlerMu.RUnlock()
	}
}

func (s *BalancedRRTransportSuite) SetupSuite() {
	s.ctx, s.cancelFn = context.WithCancel(context.TODO())

	s.backendSRVWatcher = &mockSRVWatcher{
		backendAddrUpdatesCh: make(chan []*naming.Update),
	}
	var err error
	// add `testBackendCount` backends to which we will be sending requests.
	for i := 0; i < testBackendCount; i++ {
		s.backends = append(s.backends, httptest.NewServer(s.mockedHandler(i)))
		// This may be racy, no synchronization for server start in the httptest :(
		time.Sleep(25 * time.Millisecond)
	}
	s.policy = RoundRobinPolicy(s.ctx, testFailBlacklistDuration, testDialTimeout).(*roundRobinPolicy)
	s.lbTrans, err = New(
		s.ctx,
		"my-magic-srv",
		http.DefaultTransport,
		s, // self implements naming.Resolver
		s.policy,
	)
	require.NoError(s.T(), err, "cannot fail on initialization")

	// This will update LB internal SRV lookup with new backends.
	s.backendSRVWatcher.UpdateBackends(s.backends)
	s.waitForSRVPropagation(s.backends, 1*time.Second)
}

func (s *BalancedRRTransportSuite) waitForSRVPropagation(backends []*httptest.Server, timeout time.Duration) {
	backLookupMap := map[string]struct{}{}
	for _, b := range backends {
		backLookupMap[b.Listener.Addr().String()] = struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.TODO(), timeout)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			s.T().Errorf("Timeout on waiting for new SRV changes. Given backend list never appeared in lbtransport.")
			return
		default:
		}

		time.Sleep(25 * time.Millisecond)
		foundAll := true
		s.lbTrans.mu.Lock()
		for _, t := range s.lbTrans.currentTargets {
			if _, ok := backLookupMap[t.DialAddr]; !ok {
				foundAll = false
				break
			}
		}
		if len(backends) != len(s.lbTrans.currentTargets) {
			// There are even too many backends.
			foundAll = false
		}
		s.lbTrans.mu.Unlock()

		if foundAll {
			return
		}
	}
}

func (s *BalancedRRTransportSuite) SetupTest() {
	// reset the backendHandler
	s.setBackendHandler(func(resp http.ResponseWriter, req *http.Request) {
		resp.WriteHeader(200)
	})
	s.backendSRVWatcher.UpdateBackends(s.backends)
	s.waitForSRVPropagation(s.backends, 1*time.Second)
}

func (s *BalancedRRTransportSuite) setBackendHandler(handler http.HandlerFunc) {
	s.backendHandlerMu.Lock()
	s.backendHandler = handler
	s.backendHandlerMu.Unlock()
}

func (s *BalancedRRTransportSuite) TearDownSuite() {
	for _, b := range s.backends {
		b.Close()
	}

	s.cancelFn()
}

const requestedBackendMinimumShare = 0.3 // Relatively to equal share among backends.

func (s *BalancedRRTransportSuite) callLBTransportAndAssert(requestsPerBackend int, backendsCount int) {
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
	client := &http.Client{Transport: s.lbTrans, Timeout: 10 * time.Second}
	for i := 0; i < requestsPerBackend*backendsCount; i++ {
		wg.Add(1)
		go func(id int) {
			resp, err := client.Get("http://my-magic-srv/something")
			if err != nil {
				s.T().Errorf("Encountered error on request %v: %v", id, err)
			}
			_, _  = ioutil.ReadAll(resp.Body)
			resp.Body.Close()
			wg.Done()
		}(i)
	}

	wg.Wait()

	backendMapMu.RLock()
	defer backendMapMu.RUnlock()
	assert.Equal(s.T(), backendsCount, len(backendMap), "srvlb should round robin across all backends. Got: %v", backendMap)

	// This is to eliminate flakiness of this test on slow CI. We all guaranteeing statistical round robin only.
	// NOTE: On fast machine doing requestedBackendMinimumShare = 1.0 should always work.
	requestedMinimumCalls := int(requestedBackendMinimumShare * float64(requestsPerBackend))
	for k, val := range backendMap {
		assert.True(s.T(), requestedMinimumCalls < val, "backend %v is expected to have its minimum share %d of requests. Got: %d", k, requestedMinimumCalls, val)
	}
}

func (s *BalancedRRTransportSuite) TestRoundRobinSendsRequestsToAllBackends() {
	s.callLBTransportAndAssert(50, testBackendCount)
}

func (s *BalancedRRTransportSuite) TestRoundRobinRetryRequestsForInvalidBackends() {
	// For this test add fake backend with rekt listener.
	backends := append(s.backends, httptest.NewUnstartedServer(nil))
	// Don't even listen on socket - we want dial to fail immediately.
	backends[len(backends)-1].Close()

	s.backendSRVWatcher.UpdateBackends(backends)
	s.waitForSRVPropagation(backends, 1*time.Second)

	now := time.Now()
	s.policy.timeNow = func() time.Time {
		return now
	}

	// Let's do 50*(old testBackendCount) requests. Even with the failing one we should have 50 per valid backend.
	s.callLBTransportAndAssert(50, testBackendCount)

	// this may be racy, no synchronization in the httptest :(
	backends = append(s.backends, httptest.NewServer(s.mockedHandler(testBackendCount+1)))
	defer backends[len(backends)-1].Close()

	// Does not update stuff.
	s.backendSRVWatcher.UpdateBackends(backends)
	s.waitForSRVPropagation(backends, 1*time.Second)

	s.policy.timeNow = func() time.Time {
		// Let's make blacklist obsolete.
		return now.Add(testFailBlacklistDuration).Add(5 * time.Millisecond)
	}

	// Now all servers are expected to work and each of them should get 50.
	s.callLBTransportAndAssert(50, testBackendCount+1)
}

func (s *BalancedRRTransportSuite) TestSrvLbErrorsNoResolution() {
	// No backends.
	s.backendSRVWatcher.UpdateBackends([]*httptest.Server{})
	s.waitForSRVPropagation([]*httptest.Server{}, 1*time.Second)
	client := &http.Client{Transport: s.lbTrans, Timeout: 1 * time.Second}
	req, err := http.NewRequest("GET", "http://my-magic-srv/something", nil)
	s.Require().NoError(err)

	t := &reporter.Tracker{}
	_, err = client.Do(reporter.ReqWrappedWithTracker(req, t))
	s.Require().Error(err, "srvlb should not be able to start dialing, because of no resolution")
	s.Assert().Equal(errtypes.NoResolutionAvailable, t.ErrType())
}

func (s *BalancedRRTransportSuite) TestSrvLbErrorsAllResolvedAddressesAreWrong() {
	// Add 5 not accessible backends.
	backends := []*httptest.Server{}
	for i := 0; i < 5; i++ {
		b := httptest.NewUnstartedServer(nil)
		// Don't even listen on socket - we want dial to fail immediately.
		b.Close()
		backends = append(backends, b)
	}

	// No backends.
	s.backendSRVWatcher.UpdateBackends(backends)
	s.waitForSRVPropagation(backends, 1*time.Second)
	client := &http.Client{Transport: s.lbTrans, Timeout: 1 * time.Second}
	req, err := http.NewRequest("GET", "http://my-magic-srv/something", nil)
	s.Require().NoError(err)

	t := &reporter.Tracker{}
	_, err = client.Do(reporter.ReqWrappedWithTracker(req, t))
	s.Require().Error(err, "srvlb should not be able to dial any target")
	s.Assert().Equal(errtypes.NoConnToAllResolvedAddresses, t.ErrType())
}

// mockSRVWatcher implements naming.Watcher that is used inside lbtransport to watch for SRV lookup changes.
type mockSRVWatcher struct {
	currentBackendsMap   map[string]struct{}
	backendAddrUpdatesCh chan []*naming.Update
}

// UpdateBackends update SRV targets.
func (t *mockSRVWatcher) UpdateBackends(newBackends []*httptest.Server) {
	var updates []*naming.Update

	newBackendsMap := make(map[string]struct{})
	for _, nb := range newBackends {
		newBackendsMap[nb.Listener.Addr().String()] = struct{}{}
		if _, ok := t.currentBackendsMap[nb.Listener.Addr().String()]; ok {
			continue
		}
		updates = append(updates, &naming.Update{
			Addr: nb.Listener.Addr().String(),
			Op:   naming.Add,
		})
	}

	for cbAddr := range t.currentBackendsMap {
		if _, ok := newBackendsMap[cbAddr]; ok {
			continue
		}
		updates = append(updates, &naming.Update{
			Addr: cbAddr,
			Op:   naming.Delete,
		})
	}

	t.backendAddrUpdatesCh <- updates
	t.currentBackendsMap = newBackendsMap
}

func (t *mockSRVWatcher) Next() ([]*naming.Update, error) {
	return <-t.backendAddrUpdatesCh, nil
}

func (t *mockSRVWatcher) Close() {
	close(t.backendAddrUpdatesCh)
}
