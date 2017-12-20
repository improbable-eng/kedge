# Winch

![winch](winch.jpg)

Forward proxy for gRPC, HTTP (1.1/2) microservices used as a local proxy to the clusters with the kedge at the edges.
This allows to have safe route to the internal services by the authorized user.

## Configuration

Winch is driven mainly via two configuration files.

Auth configuration that allows to configure all used authorization methods for both proxy auth and backend auth (!)
NOTE: Backend auth in some cases is required to be specified here, since most clients blocks sending auth headers using plaintext (which is reasonanble)
Since we are doing plain HTTP (and insecure gRPC) we need to sometimes inject auth on winch side.

`--server_auth_config` command line content or read from file using `--server_auth_config_path`

```json
{
  "auth_sources": [
      {
        "name": "k8s",
        "kube": {
          "user": "k8s_access"
        }
      },
      {
        "name": "kedge-prod",
        "oidc": {
          "provider": "provider",
          "client_id": "id",
          "secret": "secret",
          "scopes": [ "openid", "groups"],
          "path": "$HOME/.winch_oidc_keys"
        }
      }
    ]
}
```
The most useful one if you are using Kubernetes is `kube` auth source that allows you to specify `user` straight from your kube config.

Request Mapping:
`--server_mapper_config` command line content or read from file using `--server_mapper_config_path`
```json
{
  "routes": [
    {
    "regexp": {
      "exp": "^master[.](?P<cluster>h-[a-z0-9].*|[a-z0-9].*-prod)[.]internal[.]improbable[.]io:6443",
      "url": "https://${cluster}.kedge.improbable.io:443"
    },
    "proxy_auth": "kedge-prod",
    "backend_auth": "k8s"
    },
    {
      "regexp": {
        "exp": "([a-z0-9-].*)[.](?P<cluster>[a-z0-9-].*)[.]internal[.]example[.]com",
        "url": "https://${cluster}.kedge.ext.example.com:443"
      },
      "proxy_auth": "kedge-prod"
    }
  ]
}
```

This regexes ensures that if the request is towards Kubernetes API, it needs to be have "k8s" auth injected (since kubectl does not send auth through
plain HTTP) as well as proxy auth. Any other internal request should have only proxy auth injected.


## Usage
1. Specify rules for routing to proper kedges.
2. Run application on you local machine:

```bash
go run ./winch/server/*.go \
  --server_http_port=8098 \
  --pac_redirect_sh_expressions="*.*.internal.example.com" \
  --server_mapper_config_path=misc/winch_mapper.json \
  --server_auth_config_path=misc/winch_auth.json
```

3. Forward traffic to the `http://127.0.0.1:8098`

### Forwarding from browser

Winch implements WPAD endpoint, so on most of the application (e.g MAC Network)
you can easily set `http://127.0.0.1:8098/wpad.dat` as your Automatic Proxy Configuration.

Otherwise you need put PAC generated through `http://127.0.0.1:8098/wpad.dat` manually
or write your own PAC file.

### Forwarding from CLI 

To force an application to dial required URL through winch just set `HTTP_PROXY` environment variable to the winch localhost address.

### Forwarding from Golang

gRPC


## Debugging

Request ID
## Status

See [CHANGELOG](../CHANGELOG.md)


