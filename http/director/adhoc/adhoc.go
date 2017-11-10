package adhoc

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"sync"

	pb "github.com/improbable-eng/kedge/_protogen/kedge/config/http/routes"
	"github.com/improbable-eng/kedge/http/director/router"
)

var (
	// DefaultALookup is the lookup resolver for DNS A records.
	// You can override it for caching or testing.
	DefaultALookup = net.LookupAddr
)

// Addresser implements logic that decides what "ad-hoc" ip:port to dial for a backend, if any.
//
// Adhoc rules are a way of forwarding requests to services that fall outside of pre-defined Routes and Backends.
type Addresser interface {
	// Address decides the ip:port to send the request to, if any. Errors may be returned if permission is denied.
	// The returned string must contain contain both ip and port separated by colon.
	Address(r *http.Request) (string, error)
}

type dynamic struct {
	mu        sync.RWMutex
	addresser *static
}

// NewDynamic creates a new dynamic router that can be have its routes updated.
func NewDynamic() *dynamic {
	return &dynamic{addresser: NewStaticAddresser([]*pb.Adhoc{})}
}

func (d *dynamic) Address(req *http.Request) (string, error) {
	d.mu.RLock()
	addresser := d.addresser
	d.mu.RUnlock()
	return addresser.Address(req)
}

// Update sets addresser behaviour to the provided set of adhoc rules.
func (d *dynamic) Update(rules []*pb.Adhoc) {
	addresser := NewStaticAddresser(rules)
	d.mu.Lock()
	d.addresser = addresser
	d.mu.Unlock()
}

type static struct {
	rules []*pb.Adhoc
}

func NewStaticAddresser(rules []*pb.Adhoc) *static {
	return &static{rules: rules}
}

func (a *static) Address(req *http.Request) (string, error) {
	hostName, port, err := a.extractHostPort(req.URL.Host)
	if err != nil {
		return "", err
	}
	for _, rule := range a.rules {
		if !a.hostMatches(hostName, rule.DnsNameMatcher) {
			continue
		}
		portForRule := port
		if port == 0 {
			if defPort := rule.Port.Default; defPort != 0 {
				portForRule = int(defPort)
			} else {
				portForRule = 80
			}
		}
		if !a.portAllowed(portForRule, rule.Port) {
			return "", router.NewError(http.StatusBadRequest, fmt.Sprintf("port %d is not allowed", portForRule))
		}
		ipAddr, err := a.resolveHost(hostName)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(ipAddr, strconv.FormatInt(int64(portForRule), 10)), nil

	}
	return "", router.ErrRouteNotFound
}

func (*static) resolveHost(hostStr string) (string, error) {
	addrs, err := DefaultALookup(hostStr)
	if err != nil {
		return "", router.NewError(http.StatusBadGateway, "cannot resolve host")
	}
	return addrs[0], nil
}

func (*static) extractHostPort(hostStr string) (hostName string, port int, err error) {
	// Using SplitHostPort is a pain due to opaque error messages. Let's assume we only do hostname matches, they fall
	// through later anyway.
	portOffset := strings.LastIndex(hostStr, ":")
	if portOffset == -1 {
		return hostStr, 0, nil
	}
	portPart := hostStr[portOffset+1:]
	pNum, err := strconv.ParseInt(portPart, 10, 32)
	if err != nil {
		return "", 0, router.NewError(http.StatusBadRequest, fmt.Sprintf("malformed port number: %v", err))
	}
	return hostStr[:portOffset], int(pNum), nil
}

func (*static) hostMatches(host string, matcher string) bool {
	if matcher == "" {
		return false
	}
	if matcher[0] != '*' {
		return host == matcher
	}
	return strings.HasSuffix(host, matcher[1:])
}

func (*static) portAllowed(port int, portRule *pb.Adhoc_Port) bool {
	uPort := uint32(port)
	for _, p := range portRule.Allowed {
		if p == uPort {
			return true
		}
	}
	for _, r := range portRule.AllowedRanges {
		if r.From <= uPort && uPort <= r.To {
			return true
		}
	}
	return false
}
