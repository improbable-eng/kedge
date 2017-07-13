package kedge_map

type simple struct {
	mapping map[string]*Route
}

// Simple is a kedge mapper that returns route based on map provided in constructor.
func Simple(mapping map[string]*Route) Mapper {
	return &simple{mapping: mapping}
}

func (s *simple) Map(targetAuthorityDnsName string) (*Route, error) {
	r, ok := s.mapping[targetAuthorityDnsName]
	if !ok {
		return nil, ErrNotKedgeDestination
	}

	return r, nil
}
