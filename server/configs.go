package main

import (
	"bytes"
	"io/ioutil"

	log "github.com/Sirupsen/logrus"

	"github.com/golang/protobuf/proto"
	"github.com/mwitkow/bazel-distcache/common/sharedflags"
	"github.com/mwitkow/go-nicejsonpb"
	pb_config "github.com/mwitkow/kedge/_protogen/kedge/config"

	grpc_bp "github.com/mwitkow/kedge/grpc/backendpool"
	grpc_router "github.com/mwitkow/kedge/grpc/director/router"
	http_bp "github.com/mwitkow/kedge/http/backendpool"
	http_router "github.com/mwitkow/kedge/http/director/router"
)

var (
	flagConfigDirectorPath = sharedflags.Set.String(
		"grpcproxy_config_director_path",
		"../misc/director.json",
		"Path to the jsonPB file configuring the director.")
	flagConfigBackendPoolPath = sharedflags.Set.String(
		"grpcproxy_config_backendpool_path",
		"../misc/backendpool.json",
		"Path to the jsonPB file configuring the backend pool.")
)

func buildRouterOrFail() (grpc_router.Router, http_router.Router) {
	cnf := &pb_config.DirectorConfig{}
	if err := readAsJson(*flagConfigDirectorPath, cnf); err != nil {
		log.Fatalf("failed reading director director config: %v", err)
	}
	grpcRouter := grpc_router.NewStatic(cnf.Grpc.Routes)
	httpRouter := http_router.NewStatic(cnf.Http.Routes)
	return grpcRouter, httpRouter
}

func buildBackendPoolOrFail() (grpc_bp.Pool, http_bp.Pool) {
	cnf := &pb_config.BackendPoolConfig{}
	if err := readAsJson(*flagConfigBackendPoolPath, cnf); err != nil {
		log.Fatalf("failed reading backend pool config: %v", err)
	}
	grpcBePool, err := grpc_bp.NewStatic(cnf.GetGrpc().GetBackends())
	if err != nil {
		log.Fatalf("failed creating grpc backend pool: %v", err)
	}
	httpBePool, err := http_bp.NewStatic(cnf.GetHttp().GetBackends())
	if err != nil {
		log.Fatalf("failed creating http backend pool: %v", err)
	}
	return grpcBePool, httpBePool
}

func readAsJson(filePath string, destination proto.Message) error {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return err
	}
	um := &nicejsonpb.Unmarshaler{AllowUnknownFields: false}
	err = um.Unmarshal(bytes.NewReader(data), destination)
	if err != nil {
		return err
	}
	return nil
}
