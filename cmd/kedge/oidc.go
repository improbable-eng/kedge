package main

import (
	"context"
	"errors"

	"github.com/Bplotka/oidc/authorize"
	"github.com/improbable-eng/kedge/pkg/sharedflags"
	"github.com/sirupsen/logrus"
)

var (
	flagOIDCProvider = sharedflags.Set.String("server_oidc_provider_url", "",
		"Expected OIDC Issuer of the request's IDToken. If empty, no OIDC authorization will be required.")
	flagOIDCClientID = sharedflags.Set.String("server_oidc_client_id", "",
		"Expected OIDC Client ID of the request`s IDToken.")
	flagOIDCPermsClaim = sharedflags.Set.String("server_oidc_perms_claim", "",
		"Name of the claim that stores user's permissions.")
	flagOIDCWhiteListPerms = sharedflags.Set.StringSlice("server_oidc_whitelist_perms", []string(nil),
		"Permissions satisfy Kedge access auth.")
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

	if len(*flagOIDCWhiteListPerms) == 0 {
		return nil, errors.New("OIDC flag validation failed. server_oidc_whitelist_perms flag cannot be empty.")
	}

	var condition []authorize.Condition
	for _, permToWhitelist := range *flagOIDCWhiteListPerms {
		condition = append(condition, authorize.Contains(permToWhitelist))
	}

	cond, err := authorize.OR(condition...)
	if err != nil {
		return nil, err
	}
	return authorize.New(
		context.Background(),
		authorize.Config{
			Provider:      *flagOIDCProvider,
			ClientID:      *flagOIDCClientID,
			PermsClaim:    *flagOIDCPermsClaim,
			PermCondition: cond,
		},
	)
}
