package kedge_tls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"

	"github.com/improbable-eng/kedge/pkg/sharedflags"
	"github.com/mwitkow/go-conntrack/connhelpers"
	log "github.com/sirupsen/logrus"
)

var (
	flagTLSClientCert = sharedflags.Set.String(
		"client_tls_cert_file", "",
		"Path to the optional PEM certificate that client will use. This is required when kedge is configured with server_tls_client_cert_required=true.")
	flagTLSClientKey = sharedflags.Set.String(
		"client_tls_key_file", ".",
		"Path to the optional PEM key for the certificate that the client will use. This is required when kedge is configured with server_tls_client_cert_required=true.")
	flagTLSRootCAFiles = sharedflags.Set.StringSlice(
		"client_tls_root_ca_files", []string{},
		"Paths (comma separated) to custom PEM certificate CA chains used for root trusted CA for server verification. If nil, root CAs will fetched from host.",
	)
	flagTLSInsecureSkipVerify = sharedflags.Set.Bool(
		"client_insecure", false,
		"	Controls whether a client verifies the server's certificate chain and host name. If is true, TLS accepts any certificate presented by the server and any host name in that certificate."+
			" In this mode, TLS is susceptible to man-in-the-middle attacks. This should be used only for testing.")
)

// BuildClientTLSConfigFromFlags creates TLS config for Kedge client.
// Used by all clients that communicates with kedge (e.g Winch and LoadTest)
func BuildClientTLSConfigFromFlags() (*tls.Config, error) {
	var tlsConfig *tls.Config

	// Add client certs if specified.
	if *flagTLSClientCert == "" || *flagTLSClientKey == "" {
		log.Info("Either key or cert for TLS client certificates are not present.")
		tlsConfig = &tls.Config{}
	} else {
		// TlsConfigForServerCerts name is misleading - it Certificates field is used for client as well when used on http.Client
		var err error
		tlsConfig, err = connhelpers.TlsConfigForServerCerts(*flagTLSClientCert, *flagTLSClientKey)
		if err != nil {
			return nil, fmt.Errorf("failed reading TLS client keys. Err: %v", err)
		}
	}
	tlsConfig.MinVersion = tls.VersionTLS12

	// Add root CA if specified.
	if len(*flagTLSRootCAFiles) > 0 {
		tlsConfig.RootCAs = x509.NewCertPool()
		for _, path := range *flagTLSRootCAFiles {
			data, err := ioutil.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("failed reading root CA file %v: %v", path, err)
			}
			if ok := tlsConfig.RootCAs.AppendCertsFromPEM(data); !ok {
				return nil, fmt.Errorf("failed processing root CA file %v", path)
			}
		}
	}

	// Set no verify if specified.
	if *flagTLSInsecureSkipVerify {
		tlsConfig.InsecureSkipVerify = true
	}
	return tlsConfig, nil
}
