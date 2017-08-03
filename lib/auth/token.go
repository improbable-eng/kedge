package auth

import "fmt"

type bearertokenSource struct {
	name  string
	token string
}

func bearertoken(name string, token string) Source {
	return &bearertokenSource{
		name:  name,
		token: token,
	}
}

func (s *bearertokenSource) Name() string {
	return s.name
}

func (s *bearertokenSource) HeaderValue() (string, error) {
	return fmt.Sprintf("Bearer %s", s.token), nil
}
