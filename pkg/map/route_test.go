package kedge_map

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIPMatch(t *testing.T) {
	rm := RouteMapper(nil)
	// IP address.
	_, err := rm.Map("1.2.30.44", "12")
	assert.Error(t, err, "route to IP should fail")
	assert.Contains(t, err.Error(), "was given IP address")

	// Not an IP address error, but is not a kedge destination.
	_, err = rm.Map("1.2.30.44abc", "12")
	assert.Error(t, err)
	assert.True(t, IsNotKedgeDestinationError(err))
}
