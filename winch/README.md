# Winch

![winch](winch.jpg)

Forward proxy for gRPC, HTTP (1.1/2) microservices used as a local proxy to the clusters with the kedge at the edges.
This allows to have safe route to the internal services by the authorized user.

## Usage
1. Specify rules for routing to proper kedges.
2. Run application on you local machine:

    ```
    go run ./winch/server/*.go \
      --server_http_port=8098 \
      --pac_redirect_sh_expressions="*.*.internal.example.com" \
      --server_mapper_config_path=./misc/winch_mapper.json
      --server_auth_config_path=./misc/winch_auth.json
    ```
3. Forward traffic to the `http://127.0.0.1:8098`

### Forwarding from browser

TBD: PAC file.

### Forwarding from CLI 

To force an application to dial required URL through winch just set `HTTP_PROXY` environment variable to the winch localhost address.
 
## Status

See [CHANGELOG](../CHANGELOG.md)


