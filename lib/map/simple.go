package kedge_map

import "fmt"

type simpleHost struct {
	mapping map[string]*Route
}

// SimpleHost is a kedge mapper that returns route based on map provided in constructor. It does not care about port.
func SimpleHost(mapping map[string]*Route) Mapper {
	return &simpleHost{mapping: mapping}
}

func (s *simpleHost) Map(targetAuthorityDnsName string, _ string) (*Route, error) {
	r, ok := s.mapping[targetAuthorityDnsName]
	if !ok {
		return nil, ErrNotKedgeDestination
	}

	return r, nil
}

type simpleHostPort struct {
	mapping map[string]*Route
}

// SimpleHostPort is a kedge mapper that returns route based on map provided in constructor.
func SimpleHostPort(mapping map[string]*Route) Mapper {
	return &simpleHostPort{mapping: mapping}
}

func (s *simpleHostPort) Map(targetAuthorityDnsName string, port string) (*Route, error) {
	r, ok := s.mapping[fmt.Sprintf("%s:%s", targetAuthorityDnsName, port)]
	if !ok {
		return nil, ErrNotKedgeDestination
	}

	return r, nil
}
