package backendpool

import (
	"testing"

	pb "github.com/mwitkow/kedge/_protogen/kedge/config/http/backends"

	"github.com/stretchr/testify/assert"
	"context"
)

func TestDynamic_Operations(t *testing.T) {
	d := NewDynamic()
	d.backendFactory = func(config *pb.Backend) (*backend, error) {
		b := &backend{config: config}
		b.ctx, b.cancel = context.WithCancel(context.Background())
		return b, nil
	}
	assert.Len(t, d.Configs(), 0, "at first there needs to be nothing")
	assert.NoError(t, d.AddOrUpdate(&pb.Backend{Name: "foobar", DisableConntracking: true}))
	assert.NoError(t, d.AddOrUpdate(&pb.Backend{Name: "carbar", DisableConntracking: true}))
	assert.Len(t, d.Configs(), 2, "we should have two")
	oldCarBar := d.backends["carbar"]
	assert.NoError(t, d.AddOrUpdate(&pb.Backend{Name: "carbar"}), "updating carbar shouldn't fail")
	assert.Error(t, oldCarBar.ctx.Err(), "oldCarBar should enter closed state")
	assert.Len(t, d.Configs(), 2, "we should still two")
	assert.Error(t, d.Remove("nonexisting"), "removing a non existing backend should return error")
	assert.NoError(t, d.Remove("foobar"), "removing a non existing backend should return error")
	assert.Len(t, d.Configs(), 1, "we now should have two")
}
