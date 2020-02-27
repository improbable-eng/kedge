package bearertokenauth

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/Bplotka/oidc/authorize"
)

type bearerTokenAuthorizer struct {
	token string
}

// NewAuthorizer returns a new Bearer Token Authorizer that checks the provided token to the one it is initialized with.
func NewAuthorizer(token string) authorize.Authorizer {
	return &bearerTokenAuthorizer{
		token: token,
	}
}

func (b bearerTokenAuthorizer) IsAuthorized(ctx context.Context, token string) error {
	if token != b.token {
		return grpc.Errorf(codes.Unauthenticated, "provided bearer token is invalid")
	}
	return nil
}
