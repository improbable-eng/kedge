package main

import (
	"bytes"
	"io/ioutil"

	log "github.com/Sirupsen/logrus"

	"github.com/golang/protobuf/proto"
	"github.com/mwitkow/bazel-distcache/common/sharedflags"
	"github.com/mwitkow/go-nicejsonpb"
	pb_config "github.com/mwitkow/kfe/_protogen/kfe/config"

	grpc_bp "github.com/mwitkow/kfe/grpc/backendpool"
	grpc_router "github.com/mwitkow/kfe/grpc/director/router"
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

func buildGrpcRouterOrFail() grpc_router.Router {
	cnf := &pb_config.DirectorConfig{}
	if err := readAsJson(*flagConfigDirectorPath, cnf); err != nil {
		log.Fatalf("failed reading director director config: %v", err)
	}
	r := grpc_router.NewStatic(cnf.Grpc.Routes)
	return r
}

func buildGrpcBackendPoolOrFail() grpc_bp.Pool {
	cnf := &pb_config.BackendPoolConfig{}
	if err := readAsJson(*flagConfigBackendPoolPath, cnf); err != nil {
		log.Fatalf("failed reading backend pool config: %v", err)
	}
	bePool, err := grpc_bp.NewStatic(cnf.GetGrpc().Backends)
	if err != nil {
		log.Fatalf("failed creating backend pool: %v", err)
	}
	return bePool
}

func buildHttpRouterOrFail() grpc_router.Router {
	cnf := &pb_config.DirectorConfig{}
	if err := readAsJson(*flagConfigDirectorPath, cnf); err != nil {
		log.Fatalf("failed reading director director config: %v", err)
	}
	r := grpc_router.NewStatic(cnf.Grpc.Routes)
	return r
}

func buildHttpBackendPoolOrFail() grpc_bp.Pool {
	cnf := &pb_config.BackendPoolConfig{}
	if err := readAsJson(*flagConfigBackendPoolPath, cnf); err != nil {
		log.Fatalf("failed reading backend pool config: %v", err)
	}
	bePool, err := grpc_bp.NewStatic(cnf.GetGrpc().Backends)
	if err != nil {
		log.Fatalf("failed creating backend pool: %v", err)
	}
	return bePool
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
