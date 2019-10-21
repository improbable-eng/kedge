package grpc_winch

import (
	"fmt"

	"crypto/tls"
	"net"

	"os"

	"strings"

	"github.com/google/uuid"
	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/improbable-eng/kedge/pkg/grpcutils"
	"github.com/improbable-eng/kedge/pkg/http/header"
	kedge_map "github.com/improbable-eng/kedge/pkg/map"
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
func New(mapper kedge_map.Mapper, config *tls.Config, debugMode bool) proxy.StreamDirector {
	return func(ctx context.Context, fullMethodName string) (context.Context, *grpc.ClientConn, error) {
		md := metautils.ExtractIncoming(ctx)
		targetAuthority := md.Get(":authority")
		if targetAuthority == "" {
			return ctx, nil, errors.New("No :authority header. Cannot find the host")
		}

		route, err := mapper.Map(stripPort(targetAuthority), portOnly(targetAuthority))
		if err != nil {
			if kedge_map.IsNotKedgeDestinationError(err) {
				return ctx, nil, status.Error(codes.Unimplemented, err.Error())
			}
			return ctx, nil, err
		}

		tags := grpc_ctxtags.Extract(ctx)
		tags.Set("grpc.proxy.kedge", route.URL)
		tags.Set("grpc.target.authority", targetAuthority)

		// TODO(bplotka): Fix support for this on kedge side. Works only for HTTP for now.
		tags.Set(header.RequestKedgeRequestID, fmt.Sprintf("winch-%s", uuid.New().String()))
		if debugMode {
			tags.Set(header.RequestKedgeForceInfoLogs, os.ExpandEnv("winch-$USER"))
		}

		ctx = grpcutils.CloneIncomingToOutgoingMD(ctx)

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

		// TODO(bplotka): Consider connection pooling towards static number of kedges.
		conn, err := grpc.DialContext(
			ctx,
			net.JoinHostPort(route.URL.Hostname(), route.URL.Port()),
			dialOpts...,
		)
		if err != nil {
			return ctx, nil, err
		}

		go func() {
			<-ctx.Done()
			conn.Close()
		}()
		return ctx, conn, nil
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

func stripPort(hostport string) string {
	colon := strings.IndexByte(hostport, ':')
	if colon == -1 {
		return hostport
	}
	if i := strings.IndexByte(hostport, ']'); i != -1 {
		return strings.TrimPrefix(hostport[:i], "[")
	}
	return hostport[:colon]
}

func portOnly(hostport string) string {
	colon := strings.IndexByte(hostport, ':')
	if colon == -1 {
		return ""
	}
	if i := strings.Index(hostport, "]:"); i != -1 {
		return hostport[i+len("]:"):]
	}
	if strings.Contains(hostport, "]") {
		return ""
	}
	return hostport[colon+len(":"):]
}
