package winch

import (
	"testing"

	pb "github.com/mwitkow/kedge/_protogen/winch/config"
	"github.com/mwitkow/kedge/lib/map"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegexpRoute(t *testing.T) {
	r, err := newRegexp(&pb.RegexpRoute{
		Exp: `(?P<arg1>[a-z0-9-].*)[.](?P<ARG2>[a-z0-9-].*)[.]internal[.]example[.]org`,
		Url: "${arg1}-${ARG2}",
	}, &kedge_map.Route{})
	require.NoError(t, err)

	_, ok, err := r.Route("value1.value2.internal.unknown.org")
	require.NoError(t, err)
	assert.False(t, ok)

	route, ok, err := r.Route("value1.value2.internal.example.org")
	require.NoError(t, err)
	require.True(t, ok)

	assert.Equal(t, "value1-value2", route.URL.String())
}

func TestDirectRoute(t *testing.T) {
	r, err := newDirect(&pb.DirectRoute{
		Key: "value1.value2.internal.example.org",
		Url: "some-url.com",
	}, &kedge_map.Route{})
	require.NoError(t, err)

	_, ok, err := r.Route("value1.value2.internal.unknown.org")
	require.NoError(t, err)
	assert.False(t, ok)

	route, ok, err := r.Route("value1.value2.internal.example.org")
	require.NoError(t, err)
	require.True(t, ok)

	assert.Equal(t, "some-url.com", route.URL.String())
}
