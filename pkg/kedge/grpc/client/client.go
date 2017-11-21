package kedge_grpc

import (
	"crypto/tls"
	"errors"

	"github.com/improbable-eng/kedge/pkg/map"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// DialThroughKedge provides a grpc.ClientConn that forwards all traffic to the specific kedge.
//
// For secure kedging (with a mapper value starting with https://) clientTls option needs to be set, and needs to point
// to a tls.Config that has client side certificates configured.
func DialThroughKedge(ctx context.Context, targetAuthority string, clientTls *tls.Config, mapper kedge_map.Mapper, grpcOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	// No need for port matching here. TODO: Add when required.
	kedgeUrl, err := mapper.Map(targetAuthority, "")
	if err != nil {
		return nil, err
	}
	if kedgeUrl.URL.Scheme == "https" {
		if clientTls == nil || (len(clientTls.Certificates) == 0 && clientTls.GetCertificate == nil) {
			return nil, errors.New("dialing through kedge requires a tls.Config with client-side certificates")
		}
		transportCreds := credentials.NewTLS(clientTls)
		transportCreds = &proxiedTlsCredentials{TransportCredentials: transportCreds, authorityNameOverride: targetAuthority}
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(transportCreds))
	} else {
		grpcOpts = append(grpcOpts, grpc.WithInsecure(), grpc.WithAuthority(targetAuthority))
	}
	// TODO(mwitkow): Add pooled dialing a-la: https://sourcegraph.com/github.com/google/google-api-go-client@master/-/blob/internal/pool.go#L25:1-27:32
	return grpc.DialContext(ctx, kedgeUrl.URL.Host, grpcOpts...)
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
