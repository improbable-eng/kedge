package common

import (
	"net"
	"strconv"
	"strings"
	"sync"

	pb "github.com/improbable-eng/kedge/protogen/kedge/config/common"
	"github.com/pkg/errors"
)

var (
	// DefaultALookup is the lookup resolver for DNS A records.
	// You can override it for caching or testing.
	DefaultALookup = net.LookupHost
)

// Addresser implements logic that decides what "ad-hoc" ip:port to dial for a backend, if any.
//
// Adhoc rules are a way of forwarding requests to services that fall outside of pre-defined Routes and Backends.
type Addresser interface {
	// Address decides the ip:port to send the request to, if any. Errors may be returned if permission is denied.
	// The returned string must contain contain both ip and port separated by colon.
	Address(hostString string) (string, error)
}

type dynamic struct {
	mu              sync.RWMutex
	staticAddresser Addresser
}

// NewDynamic creates a new dynamic router that can be have its routes updated.
func NewDynamic(add Addresser) *dynamic {
	return &dynamic{staticAddresser: add}
}

func (d *dynamic) Address(hostString string) (string, error) {
	d.mu.RLock()
	addresser := d.staticAddresser
	d.mu.RUnlock()
	return addresser.Address(hostString)
}

// Update sets addresser behaviour to the provided set of adhoc rules.
func (d *dynamic) Update(add Addresser) {
	d.mu.Lock()
	d.staticAddresser = add
	d.mu.Unlock()
}

func ExtractHostPort(hostStr string) (hostName string, port int, err error) {
	// Using SplitHostPort is a pain due to opaque error messages. Let's assume we only do hostname matches, they fall
	// through later anyway.
	portOffset := strings.LastIndex(hostStr, ":")
	if portOffset == -1 {
		return hostStr, 0, nil
	}
	portPart := hostStr[portOffset+1:]
	pNum, err := strconv.ParseInt(portPart, 10, 32)
	if err != nil {
		return "", 0, err
	}
	return hostStr[:portOffset], int(pNum), nil
}

func HostMatches(host string, matcher string) bool {
	if matcher == "" {
		return false
	}
	if matcher[0] != '*' {
		return host == matcher
	}
	return strings.HasSuffix(host, matcher[1:])
}

func PortAllowed(port int, portRule *pb.Adhoc_Port) bool {
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

func AdhocResolveHost(hostStr string, replace *pb.Adhoc_Replace) (string, error) {
	if replace != nil {
		if !strings.Contains(hostStr, replace.Pattern) {
			return "", errors.Errorf("replace pattern %s does match given host %s. Configuration error", replace.Pattern, hostStr)
		}

		hostStr = strings.Replace(hostStr, replace.Pattern, replace.Substitution, -1)
	}
	addrs, err := DefaultALookup(hostStr)
	if err != nil {
		return "", err
	}
	if len(addrs) == 0 {
		return "", errors.Errorf("did not find any A records for host %v", hostStr)
	}
	return addrs[0], nil
}
