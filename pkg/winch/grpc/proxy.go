package grpc_winch

import (
	"fmt"

	"crypto/tls"
	"net"

	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/improbable-eng/kedge/pkg/map"
	"github.com/improbable-eng/kedge/pkg/tokenauth"
	"github.com/mwitkow/grpc-proxy/proxy"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// New builds a StreamDirector based off a backend pool and a router.
func New(mapper kedge_map.Mapper, config *tls.Config) proxy.StreamDirector {
	return func(ctx context.Context, fullMethodName string) (*grpc.ClientConn, error) {
		md := metautils.ExtractIncoming(ctx)
		targetAuthority := md.Get(":authority")
		if targetAuthority == "" {
			return nil, errors.New("No :authority header. Cannot find the host")
		}

		route, err := mapper.Map(targetAuthority, "")
		if err != nil {
			if err == kedge_map.ErrNotKedgeDestination {
				return nil, status.Error(codes.Unimplemented, err.Error())
			}
			return nil, err
		}

		grpc_ctxtags.Extract(ctx).Set("grpc.proxy.kedge", route.URL)

		transportCreds := credentials.NewTLS(config)
		// Make sure authority is ok.
		transportCreds = &proxiedTlsCredentials{TransportCredentials: transportCreds, authorityNameOverride: targetAuthority}

		// NOTE: winch does not support non-TLS kedge explicitly.
		dialOpts := []grpc.DialOption{
			grpc.WithCodec(proxy.Codec()), // needed for the winch to function at all.
			grpc.WithBlock(),
			grpc.WithTransportCredentials(transportCreds),
		}

		if route.ProxyAuth != nil {
			dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(newTokenCredentials(route.ProxyAuth, "proxy-authorization")))
		}
		if route.BackendAuth != nil {
			dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(newTokenCredentials(route.BackendAuth, "authorization")))
		}

		return grpc.DialContext(
			ctx,
			net.JoinHostPort(route.URL.Hostname(), route.URL.Port()),
			dialOpts...,
		)
	}
}

type proxiedTlsCredentials struct {
	credentials.TransportCredentials
	authorityNameOverride string
}

func (c *proxiedTlsCredentials) Info() credentials.ProtocolInfo {
	info := c.TransportCredentials.Info()
	// gRPC Clients set authority per DialContext rules. For TLS, authority comes from ProtocolInfo.
	// This value is not overrideable inside the metadata, since two `:authority` headers would be written, causing an
	// error on the transport.
	info.ServerName = c.authorityNameOverride
	return info
}

type oidcCreds struct {
	source tokenauth.Source
	header string
}

func (c *oidcCreds) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	token, err := c.source.Token(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "cannot get token")
	}

	return map[string]string{
		c.header: fmt.Sprintf("bearer %s", token),
	}, nil
}

func (c *oidcCreds) RequireTransportSecurity() bool {
	return true
}

func newTokenCredentials(source tokenauth.Source, header string) credentials.PerRPCCredentials {
	return &oidcCreds{source: source, header: header}
}
