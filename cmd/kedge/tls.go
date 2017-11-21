package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"

	"github.com/improbable-eng/kedge/pkg/sharedflags"
	"github.com/mwitkow/go-conntrack/connhelpers"
)

var (
	flagTLSServerCert = sharedflags.Set.String(
		"server_tls_cert_file",
		"../misc/localhost.crt",
		"Path to the PEM certificate for server use.")
	flagTLSServerKey = sharedflags.Set.String(
		"server_tls_key_file",
		"../misc/localhost.key",
		"Path to the PEM key for the certificate for the server use.")
	flagTLSServerClientCAFiles = sharedflags.Set.StringSlice(
		"server_tls_client_ca_files", []string{},
		"Paths (comma separated) to PEM certificate chains used for client-side verification. If empty, client-side verification is disabled.",
	)
	flagTLSServerClientCertRequired = sharedflags.Set.Bool(
		"server_tls_client_cert_required", true,
		"Controls whether a client certificate is required. Only used if server_tls_client_ca_files is not empty. "+
			"If true, connections that are not certified by client CA will be rejected.")
)

func buildTLSConfigFromFlags() (*tls.Config, error) {
	tlsConfig, err := connhelpers.TlsConfigForServerCerts(*flagTLSServerCert, *flagTLSServerKey)
	if err != nil {
		return nil, fmt.Errorf("failed reading TLS server keys. Err: %v", err)
	}
	tlsConfig.MinVersion = tls.VersionTLS12
	tlsConfig.ClientAuth = tls.NoClientCert

	err = addClientCertIfNeeded(tlsConfig)
	if err != nil {
		return nil, err
	}
	return tlsConfig, nil
}

func addClientCertIfNeeded(tlsConfig *tls.Config) error {
	if len(*flagTLSServerClientCAFiles) > 0 {
		if *flagTLSServerClientCertRequired {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		}
		tlsConfig.ClientCAs = x509.NewCertPool()
		for _, path := range *flagTLSServerClientCAFiles {
			data, err := ioutil.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed reading client CA file %v: %v", path, err)
			}
			if ok := tlsConfig.ClientCAs.AppendCertsFromPEM(data); !ok {
				return fmt.Errorf("failed processing client CA file %v", path)
			}
		}
	}
	return nil
}
