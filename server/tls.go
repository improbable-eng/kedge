package main

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"

	"github.com/mwitkow/go-conntrack/connhelpers"
	"github.com/mwitkow/kedge/lib/sharedflags"
	log "github.com/sirupsen/logrus"
)

var (
	flagTlsServerCert = sharedflags.Set.String(
		"server_tls_cert_file",
		"../misc/localhost.crt",
		"Path to the PEM certificate for server use.")
	flagTlsServerKey = sharedflags.Set.String(
		"server_tls_key_file",
		"../misc/localhost.key",
		"Path to the PEM key for the certificate for the server use.")
	flagTlsServerClientCAFiles = sharedflags.Set.StringSlice(
		"server_tls_client_ca_files",	[]string{},
		"Paths (comma separated) to PEM certificate chains used for client-side verification. If empty, client-side verification is disabled.",
	)
	flagTlsServerClientCertRequired = sharedflags.Set.Bool(
		"server_tls_client_cert_required",true,
		"Controls whether a client certificate is required. Only used if server_tls_client_ca_files is not empty. " +
			"If true, connections that are not certified by client CA will be rejected.")
)

func buildServerTlsOrFail() *tls.Config {
	tlsConfig, err := connhelpers.TlsConfigForServerCerts(*flagTlsServerCert, *flagTlsServerKey)
	if err != nil {
		log.Fatalf("failed reading TLS server keys: %v", err)
	}
	tlsConfig.MinVersion = tls.VersionTLS12
	tlsConfig.ClientAuth = tls.NoClientCert

	addClientCertIfNeeded(tlsConfig)
	return tlsConfig
}

func addClientCertIfNeeded(tlsConfig *tls.Config) {
	if len(*flagTlsServerClientCAFiles) > 0 {
		if *flagTlsServerClientCertRequired {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		}
		tlsConfig.ClientCAs = x509.NewCertPool()
		for _, path := range *flagTlsServerClientCAFiles {
			data, err := ioutil.ReadFile(path)
			if err != nil {
				log.Fatalf("failed reading client CA file %v: %v", path, err)
			}
			if ok := tlsConfig.ClientCAs.AppendCertsFromPEM(data); !ok {
				log.Fatalf("failed processing client CA file %v", path)
			}
		}
	}
}
