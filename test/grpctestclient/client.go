package main

import (
	"crypto/tls"
	"io"
	"net/url"
	"os"
	"time"

	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	pb_base "github.com/improbable-eng/kedge/protogen/base"
	"github.com/improbable-eng/kedge/grpc/client"
	"github.com/improbable-eng/kedge/lib/map"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

var (
	proxyHostPort = "https://127.0.0.1:8443" // use 8081 for plain text
)

func addClientCerts(tlsConfig *tls.Config) {
	cert, err := tls.LoadX509KeyPair("../../misc/client.crt", "../../misc/client.key")
	if err != nil {
		logrus.Fatal("failed loading client cert: %v", err)
	}
	tlsConfig.Certificates = []tls.Certificate{cert}
}

func main() {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // we use a self signed cert
	}
	addClientCerts(tlsConfig)
	logrus.SetOutput(os.Stdout)

	kedgeUrl, _ := url.Parse(proxyHostPort)
	conn, err := kedge_grpc.DialThroughKedge(context.TODO(), "controller.ext.cluster.local", tlsConfig, kedge_map.Single(kedgeUrl))
	if err != nil {
		logrus.Fatalf("cannot dial: %v", err)
	}
	ctx, _ := context.WithTimeout(context.TODO(), 5*time.Second)
	client := pb_base.NewServerStatusClient(conn)
	listClient, err := client.FlagzList(ctx, &google_protobuf.Empty{})
	if err != nil {
		logrus.Fatalf("request failed: %v", err)
	}
	for {
		msg, err := listClient.Recv()
		if err == io.EOF {
			break
		} else if err != nil {
			logrus.Fatalf("request failed mid way: %v", err)
		}
		logrus.Info("Flag: ", msg)
	}
}
