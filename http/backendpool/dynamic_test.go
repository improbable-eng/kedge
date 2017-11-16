package backendpool

import (
	"context"
	"testing"

	pb_resolvers "github.com/improbable-eng/kedge/protogen/kedge/config/common/resolvers"
	pb "github.com/improbable-eng/kedge/protogen/kedge/config/http/backends"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamic_Operations(t *testing.T) {
	d := NewDynamic(logrus.New())
	d.backendFactory = func(config *pb.Backend) (*backend, error) {
		b := &backend{config: config}
		b.ctx, b.cancel = context.WithCancel(context.Background())
		return b, nil
	}
	assert.Len(t, d.Configs(), 0, "at first there needs to be nothing")
	changed, err := d.AddOrUpdate(
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
	assert.Error(t, oldCarBar.ctx.Err(), "oldCarBar should enter closed state")
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
