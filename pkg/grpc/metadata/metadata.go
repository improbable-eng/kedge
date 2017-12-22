package grpc_metadata

import (
	"context"

	"google.golang.org/grpc/metadata"
)

func CloneIncomingToOutgoing(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.Pairs()
	}
	// Copy the inbound metadata explicitly.
	return metadata.NewOutgoingContext(ctx, md.Copy())
}
