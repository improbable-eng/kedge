package auth

import "errors"

type dummySource struct {
	name  string
	value string
}

func Dummy(name string, value string) Source {
	return &dummySource{
		name:  name,
		value: value,
	}
}

func (s *dummySource) Name() string {
	return s.name
}

func (s *dummySource) HeaderValue() (string, error) {
	if s.value == "" {
		// This error can be spotted in constructor but is moved here by purpose - testing.
		return "", errors.New("Dummy: Value not specified")
	}
	return s.value, nil
}
