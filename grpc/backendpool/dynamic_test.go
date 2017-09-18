package backendpool

import (
	"testing"

	pb_resolvers "github.com/mwitkow/kedge/_protogen/kedge/config/common/resolvers"
	pb "github.com/mwitkow/kedge/_protogen/kedge/config/grpc/backends"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamic_Operations(t *testing.T) {
	d := NewDynamic(logrus.New())
	d.backendFactory = func(config *pb.Backend) (*backend, error) {
		return &backend{config: config}, nil
	}
	assert.Len(t, d.Configs(), 0, "at first there needs to be nothing")
	err := d.AddOrUpdate(
		&pb.Backend{
			Name: "foobar", DisableConntracking: true, Resolver: &pb.Backend_K8S{
				K8S: &pb_resolvers.K8SResolver{
					DnsPortName: "some:port",
				},
			},
		},
		false,
	)
	require.NoError(t, err)
	assert.True(t, changed)

	changed, err = d.AddOrUpdate(&pb.Backend{Name: "carbar", DisableConntracking: true}, false)
	require.NoError(t, err)
	assert.True(t, changed)

	assert.Len(t, d.Configs(), 2, "we should have two")
	oldCarBar := d.backends["carbar"]
	changed, err = d.AddOrUpdate(&pb.Backend{Name: "carbar"}, false)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.True(t, oldCarBar.closed, "oldCarBar should enter closed state")
	assert.Len(t, d.Configs(), 2, "we should still two")

	// Add totally the same one.
	changed, err = d.AddOrUpdate(
		&pb.Backend{
			Name: "foobar", DisableConntracking: true, Resolver: &pb.Backend_K8S{
				K8S: &pb_resolvers.K8SResolver{
					DnsPortName: "some:port",
				},
			},
		},
		false,
	)
	require.NoError(t, err)
	assert.False(t, changed, "We tried to update totally the same backend, it should not change.")

	assert.Error(t, d.Remove("nonexisting"), "removing a non existing backend should return error")
	assert.NoError(t, d.Remove("foobar"), "removing a non existing backend should return error")
	assert.Len(t, d.Configs(), 1, "we now should have two")
}
