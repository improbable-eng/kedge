# Proof of concept Server

This server starts up a gRPC reverse proxy.

## Configuration

Driven through two config files: 

`--kedge_config_backendpool_config` command line content or read from file using `--kedge_config_backendpool_config_path`:
```json
{
  "grpc": {
    "backends": [
      {
        "name": "controller",
        "balancer": "ROUND_ROBIN",
        "interceptors": [
          {
            "prometheus": true
          }
        ],
        "srv": {
          "dns_name": "controller.eu1-prod.internal.improbable.io"
        }
      }
    ]
  },
  "http": {
    "backends": [
      {
        "name": "controller",
        "balancer": "ROUND_ROBIN",
        "srv": {
          "dns_name": "controller.metrics.eu1-prod.internal.improbable.io"
        }
      }
    ]
  }
}
```

`--kedge_config_director_config` command line content or read from file using `--kedge_config_director_config_path`:
```json
{
  "grpc": {
    "routes": [
      {
        "backend_name": "controller",
        "service_name_matcher": "*",
        "authority_matcher": "controller.ext.cluster.local"
      }
    ]
  },
  "http": {
    "routes": [
      {
        "backend_name": "controller",
        "host_matcher": "controller.ext.cluster.local"
      }
    ],
    "adhoc_rules": [
      {
        "dns_name_matcher": "*.pod.cluster.local",
        "port": {
          "allowed_ranges": [
            {
              "from": 40,
              "to": 10000
            }
          ]
        }
      }
     ]
  }
}
```

## Running:

Here's an example that runs the server listening on four ports (80 for debug HTTP, 443 for HTTPS+gRPCTLS, 444 for gRPCTLS, 81 for gRPC plain text), and requiring 
client side certs:
```sh
go build 
./server \
  --server_grpc_port=81 \
  --server_grpc_tls_port=444 \
  --server_http_port=80 \
  --server_http_tls_port=443 \ 
  --server_tls_cert_file=misc/localhost.crt \ 
  --server_tls_key_file=misc/localhost.key \
  --server_tls_client_ca_files=misc/ca.crt \ 
  --server_tls_client_cert_required=true \
  --kedge_config_director_config_path=../misc/director.json \
  --kedge_config_backendpool_config_path=../misc/backendpool.json 
```