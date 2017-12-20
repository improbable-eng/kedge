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

Example "through winch" gRPC dialer:
```go
// WinchDialer returns dialer function that gives you WinchDialer-enabled dialer:
// If winch.GRPCURLFromKubeConfig() gives empty string it returns pure grpc.DialContext.
// If winch.GRPCURLFromKubeConfig() gives non URL formatted string it returns error.
// If winch.GRPCURLFromKubeConfig() gives proper winch URL it gRPC dialer that proxies traffic through winch.
func WinchDialer() (dialContextFunc, error) {
	winchURL := getWinchURL()
	if winchURL == "" {
		// Not specified - nothing to put in context.
		return grpc.DialContext, nil
	}

	proxyURL, err := url.Parse(winchURL)
	if err != nil {
		return nil, errors.Wrap(err, "invalid winch URL address %q: %v", winchURL, err)
	}

	return func(ctx context.Context, targetAuthority string, grpcOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
		// NOTE: This will conflict with TLS transport credential grpc options passed by argument, but we don't have the control to validate that.
		grpcOpts = append(grpcOpts, grpc.WithInsecure())
		grpcOpts = append(grpcOpts, grpc.WithAuthority(targetAuthority))

		return grpc.DialContext(ctx, proxyURL.Host, grpcOpts...)
	}, nil
}
```
in this example `getWinchURL()` gives you winch URL if you wish to use winch, or empty string, if you wish to have direct calls.


Example HTTP proxy:
```go
type TransportProxy func(r *http.Request) (*url.URL, error)

// wrapProxy returns proxy function that returns winch URL only when:
// - given initial proxy will return nil (no proxy URL) or initial proxy function is nil.
// - Request is just plain HTTP.
// - Requested host is not localhost.
func wrapProxy(initial TransportProxy, winchURL *url.URL) TransportProxy {
	return func(req *http.Request) (*url.URL, error) {
		if initial != nil {
			proxyURL, err := initial(req)
			if err != nil {
				return nil, err
			}
			if proxyURL != nil {
				return proxyURL, nil
			}
		}

		if req.URL.Scheme == "https" {
			// Winch does not support https yet, so we don't redirect these.
			return nil, nil
		}

		if !useProxy(req.URL) {
			// Don't redirect these.
			return nil, nil
		}

		return winchURL, nil
	}
}

// useProxy reports whether requests to addr should use a proxy,
// according to the NO_PROXY or no_proxy environment variable.
// addr is always a canonicalAddr with a host and port.
func useProxy(url *url.URL) bool {
	if len(url.Hostname()) == 0 {
		return true
	}

	if url.Hostname() == "localhost" {
		return false
	}
	if ip := net.ParseIP(url.Hostname()); ip != nil {
		if ip.IsLoopback() {
			return false
		}
	}

	for _, no_proxy := range []string{
		os.Getenv(noProxyUpperCaseEnv),
		os.Getenv(noProxyLowerCaseEnv),
	} {
		if no_proxy == "*" {
			return false
		}

		for _, p := range strings.Split(no_proxy, ",") {
			p = strings.ToLower(strings.TrimSpace(p))
			if len(p) == 0 {
				continue
			}
			if url.Host == p {
				return false
			}
		}
	}
	return true
}

```

## Debugging

All request going through have Request ID propagated, so you should be able to match request just seeing the logs from winch and kedge.

Obviously DEBUG logging level is not recommended on production, however you can use `--debug-mode` flag on winch to print DEBUG logs for requests from this
particular winch as INFO on kedge.

## Status

See [CHANGELOG](../CHANGELOG.md)


