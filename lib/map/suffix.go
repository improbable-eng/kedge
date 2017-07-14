package kedge_map

import (
	"errors"
	"net/url"
	"strings"
)

type suffixMapper struct {
	reverseMatchParts []string

	suffix string
	scheme string
}

// Suffix is a `kedge.Mapper` that copies over wildcarded parts of a URL.
//
// For example: given a matchPattern of *.*.clusters.local, and a URL of `myservice.mynamespace.svc.us1.prod.clusters.local`
// and a suffixMapper of `.clusters.example.com`, it will return a kedge url of `us1.prod.clusters.example.com`.
//
// Scheme needs to be `http` or `https`
func Suffix(matchPattern string, suffix string, scheme string) (Mapper, error) {
	if !strings.HasPrefix(suffix, ".") {
		return nil, errors.New("suffixMapper needs to start with a dot")
	}
	if !strings.HasPrefix(matchPattern, "*.") {
		return nil, errors.New("matchPattern must start with a wildcard match *.")
	}
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("scheme must be http or https")
	}
	return &suffixMapper{
		reverseMatchParts: reverse(strings.Split(matchPattern, ".")),
		suffix:            suffix,
		scheme:            scheme,
	}, nil
}

func (s *suffixMapper) Map(targetAuthorityDnsName string) (*Route, error) {
	reverseTargetParts := reverse(strings.Split(targetAuthorityDnsName, "."))
	if len(reverseTargetParts) < len(s.reverseMatchParts) {
		return nil, ErrNotKedgeDestination // target is shorter than the match, definitely no point.
	}
	wildcardParts := []string{}
	for i, part := range s.reverseMatchParts {
		if part == "*" {
			wildcardParts = append(wildcardParts, reverseTargetParts[i])
			continue
		}
		if reverseTargetParts[i] != part {
			return nil, ErrNotKedgeDestination
		}
	}
	kedgeUrl := s.scheme + "://" + strings.Join(reverse(wildcardParts), ".") + s.suffix

	u, err := url.Parse(kedgeUrl)
	if err != nil {
		return nil, err
	}
	return &Route{URL: u}, nil
}

func reverse(strings []string) []string {
	for i, j := 0, len(strings)-1; i < j; i, j = i+1, j-1 {
		strings[i], strings[j] = strings[j], strings[i]
	}
	return strings
}
