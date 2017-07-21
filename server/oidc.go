package main

import (
	"context"
	"errors"

	"github.com/Bplotka/oidc/authorize"
	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/sirupsen/logrus"
)

var (
	flagOIDCProvider = sharedflags.Set.String("server_oidc_provider_url", "",
		"Expected OIDC Issuer of the request's IDToken. If empty, no OIDC authorization will be required.")
	flagOIDCClientID = sharedflags.Set.String("server_oidc_client_id", "",
		"Expected OIDC Client ID of the request`s IDToken.")
	flagOIDCPermsClaim = sharedflags.Set.String("server_oidc_perms_claim", "",
		"Name of the claim that stores user's permissions.")
	flagOIDCRequiredPerms = sharedflags.Set.String("server_oidc_required_perm", "",
		"Permissions which is required to access kedge.")
	flagEnableOIDCAuthForDebugEnpoints = sharedflags.Set.Bool("server_enable_oidc_for_debug_endpoints", false,
		"If true, debug endpoints will be hidden by OIDC Auth with the same configuration as proxy.")
)

func authorizerFromFlags(entry *logrus.Entry) (authorize.Authorizer, error) {
	if *flagOIDCProvider == "" {
		entry.Warn("No OIDC authorization is configured.")
		return nil, nil
	}
	if *flagOIDCClientID == "" {
		return nil, errors.New("OIDC flag validation failed. server_oidc_client_id is missing.")
	}

	if *flagOIDCPermsClaim == "" {
		return nil, errors.New("OIDC flag validation failed. server_oidc_perms_claim flag is missing.")
	}

	if *flagOIDCRequiredPerms == "" {
		return nil, errors.New("OIDC flag validation failed. server_oidc_required_perm flag is missing.")
	}

	return authorize.New(
		context.Background(),
		authorize.Config{
			Provider:      *flagOIDCProvider,
			ClientID:      *flagOIDCClientID,
			PermsClaim:    *flagOIDCPermsClaim,
			RequiredPerms: *flagOIDCRequiredPerms,
		},
	)
}
