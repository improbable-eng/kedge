# Kedge Load Tester

This tool is aimed to perform stress test of target Kedge endpoint. It allows
to set up X workers that will perform simple GET HTTP request for defined resource every Y seconds. The goal is to
test how many QPS (queries per second) Kedge with or without Winch can handle.

By specifying duration of the test, we calculate QPS from single load test as (X / Y) QPS.

## Usage

The main required setup is to define what will be you backend you are accessing. It needs to be a quick, lightweight resource.
Any kubernetes health endpoint seems reasonable, but make sure to not use Kedge own healthz endpoint, to have just proxy
results (not impacted by handling healthz load). It is recommended to use lot's of replicas of the backend to actually hit
Kedge limits.

NOTE: ulimit commands are to increase number of file descriptors we can open. You might want to increase hard limit of these if needed. 
NOTE: we are using health endpoint, so our expected response is OK response from it. 

### Through Winch
This test will pass all request through winch. This is useful to estimate where is the bottleneck in your setup.

```
ulimit -n 20000
go run tools/loadtest/*.go \
  --winch_url=http://127.0.0.1:8070 \
  --scenario_yaml='
target_url: http://<target> 
duration: 5m
workers: 300
tick_on: 1s
expected_response: "{\"is_ok\":true}"'
```
This will start 300 go routines that will try to GET target every second through winch to maintain 300 QPS for 5 minutes.
In this case your local winch is deciding what kedge we load test.

### Directly Kedge
In this mode, loadtest will behave as winch, so it requires almost the same flags (except mapper.json) 

```
ulimit -n 20000
go run tools/loadtest/*.go \
  --kedge_url=<kedge URL> \
  --auth_config_path=<winch_auth.json> \
  --client_tls_root_ca_files=<root-ca.crt> \
  --auth_source_name=<auth name to use against kedge if any> \
  --scenario_yaml='
target_url: http://<target> 
duration: 5m
workers: 300
tick_on: 1s
expected_response: "{\"is_ok\":true}"'
```
This will start 300 go routines that will try to GET target every second against target Kedge to maintain 300 QPS for 5 minutes.

### Remote test

In root directory you can find `Dockerfile_loadtest` that enables to build an interactive container and spin that as a pod e.g like this:
```
docker build -t "<image_name>" -f Dockerfile_loadtester .
# Push your docker image to the place accessible by your k8s

uuid=$(uuidgen | cut -c1-5)
kubectl run kedge-loadtest-${uuid} \
    --rm -i --tty -v <add here directory with auth.json and root-ca.crt if needed> --image "<image_name>" -- bash
```

It is interactive to make sure you start all clients in the same time and gather output (there is no sync between loadtest instances)

## Output

Output is in form of Prometheus metrics with aggregation of errors found during test. 

## TODO:
* [ ] Prometheus scrape endpoint to allow any Prometheus to check load test results.