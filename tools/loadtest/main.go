package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/improbable-eng/go-httpwares/metrics"
	"github.com/improbable-eng/kedge/lib/http/header"
	"github.com/improbable-eng/kedge/lib/http/tripperware"
	"github.com/improbable-eng/kedge/lib/map"
	"github.com/improbable-eng/kedge/lib/sharedflags"
	"github.com/improbable-eng/kedge/lib/tls"
	"github.com/improbable-eng/kedge/lib/winch"
	pb_config "github.com/improbable-eng/kedge/protogen/winch/config"
	"github.com/mwitkow/go-flagz"
	"github.com/mwitkow/go-flagz/protobuf"
	"github.com/mwitkow/go-proto-validators"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

var (
	flagLogLevel = sharedflags.Set.String("log_level", "info", "Log level")
	flagScenario = sharedflags.Set.String("scenario_yaml", "", "Required flag. Scenario yaml content describing the test.")

	flagWinchURL = sharedflags.Set.String("winch_url", "http://127.0.0.1:8070", "If specified, "+
		"provided winch URL will be used on every load testing call.")

	flagKedgeURL = sharedflags.Set.String("kedge_url", "", "If specified and winch_url is empty, "+
		"provided will be used to target all request as kedge endpoint.")
	flagHttpPort   = sharedflags.Set.Int("server_http_port", 8111, "TCP port to listen on for OIDC callback when non-winch mode is enabled.")
	flagAuthConfig = protoflagz.DynProto3(sharedflags.Set,
		"auth_config",
		&pb_config.AuthConfig{},
		"Contents of the Winch Auth configuration. Content or read from file if _path suffix. Required to authorize kedge").
		WithFileFlag("../../misc/winch_auth.json").WithValidator(validateMapper)
	flagAuthSourceName = sharedflags.Set.String("auth_source_name", "", "Actual auth source name to use from --auth_config")
)

func validateMapper(msg proto.Message) error {
	if val, ok := msg.(validator.Validator); ok {
		if err := val.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := sharedflags.Set.Parse(os.Args); err != nil {
		logrus.WithError(err).Fatal("failed parsing flags")
	}

	if err := flagz.ReadFileFlags(sharedflags.Set); err != nil {
		logrus.WithError(err).Fatal("failed reading flagz from files")
	}

	lvl, err := logrus.ParseLevel(*flagLogLevel)
	if err != nil {
		logrus.WithError(err).Fatalf("Cannot parse log level: %s", *flagLogLevel)
	}
	logrus.SetLevel(lvl)
	logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	if *flagScenario == "" {
		logrus.Fatal("--scenario_yaml flag is required. See --help for details")
	}

	s := scenario{}
	err = yaml.Unmarshal([]byte(*flagScenario), &s)
	if err != nil {
		logrus.WithError(err).Fatalf("Cannot parse --scenario_yaml flag: %s", *flagScenario)
	}
	logrus.Warn("Make sure you have enough file descriptors on your machine. Run ulimit -n <value> to set it. Same for winch")

	var t *tester
	if val := *flagKedgeURL; val != "" {
		logrus.Infof("Performing test directly to kedge on %s", val)

		var authSource *pb_config.AuthSource
		for _, source := range flagAuthConfig.Get().(*pb_config.AuthConfig).AuthSources {
			if source.GetName() == *flagAuthSourceName {
				authSource = source
				break
			}
		}
		if authSource == nil {
			logrus.Fatalf("%s auth source not found in given authConfig", *flagAuthSourceName)
		}
		t, err = newLoadTesterDirectlyKedge(logrus.StandardLogger(), val, *flagHttpPort, authSource)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to prepare load tester")
		}
	} else {
		val = *flagWinchURL
		logrus.Infof("Performing test via Winch on %s", val)
		t = newLoadTesterThroughWinch(logrus.StandardLogger(), val)
	}

	// Perform Load Test.
	t.loadTest(context.Background(), s)
	logrus.Infof("Performed %s", *flagScenario)
}

type tester struct {
	logger logrus.FieldLogger
	client *http.Client
}

func newLoadTesterThroughWinch(logger logrus.FieldLogger, winchURL string) *tester {
	tr := http.DefaultTransport.(*http.Transport)
	tr.Proxy = func(_ *http.Request) (*url.URL, error) {
		return url.Parse(winchURL)
	}

	cl := &http.Client{
		Transport: http_metrics.Tripperware(&reporter{proxyAddress: winchURL})(tr),
	}
	return &tester{
		logger: logger,
		client: cl,
	}
}

func newLoadTesterDirectlyKedge(
	logger logrus.FieldLogger, kedgeURL string, listenPort int, authSource *pb_config.AuthSource,
) (*tester, error) {
	config, err := kedge_tls.BuildClientTLSConfigFromFlags()
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(kedgeURL)
	if err != nil {
		return nil, err
	}

	addr := fmt.Sprintf("%s:%d", "127.0.0.1", listenPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	source, err := winch.NewAuthFactory(listener.Addr().String(), mux).Get(authSource)
	if err != nil {
		return nil, err
	}

	// Mimick winch.
	parentTransport := tripperware.Default(config)
	parentTransport = tripperware.WrapForProxyAuth(parentTransport)
	parentTransport = tripperware.WrapForRouting(parentTransport)
	parentTransport = tripperware.WrapForMapping(
		kedge_map.SingleWithProxyAuth(u, source),
		parentTransport,
	)

	cl := &http.Client{
		Transport: http_metrics.Tripperware(&reporter{proxyAddress: kedgeURL})(parentTransport),
	}
	return &tester{
		logger: logger,
		client: cl,
	}, nil
}

type scenario struct {
	TargetURL         string        `yaml:"target_url"`
	TestDuration      time.Duration `yaml:"duration"`
	TickOnEvery       time.Duration `yaml:"tick_on"`
	ConcurrentWorkers uint          `yaml:"workers"`
	ExpectedRes       string        `yaml:"expected_response"`
}

func (t *tester) loadTest(ctx context.Context, sc scenario) {
	ctx, cancel := context.WithTimeout(ctx, sc.TestDuration+1*time.Second)
	defer cancel()

	calls := uint(math.Ceil(float64(sc.TestDuration) / float64(sc.TickOnEvery)))
	qps := float64(sc.ConcurrentWorkers) / sc.TickOnEvery.Seconds()
	t.logger.Infof("Starting %v concurrent workers against target %s. Targeting %v QPS",
		sc.ConcurrentWorkers, sc.TargetURL, qps)
	now := time.Now()
	wg, errAggr := t.scheduleWorkers(ctx, sc.TargetURL, sc.ConcurrentWorkers, calls, sc.ExpectedRes, sc.TickOnEvery)
	wg.Wait()
	duration := time.Since(now)
	t.logger.Infof("Test Done in %s", duration.String())

	// Print found errors.
	errAggr.Print()

	// Print metrics
	printStats(duration)
}

func (t *tester) scheduleWorkers(
	ctx context.Context,
	targetURL string,
	workers uint,
	calls uint,
	expectedRes string,
	tickOnEvery time.Duration,
) (*sync.WaitGroup, *errAggregator) {
	test := &sync.WaitGroup{}
	aggr := &errAggregator{
		m:      map[string]uint{},
		logger: t.logger,
	}

	for i := uint(0); i < workers; i++ {
		test.Add(1)

		go func() {
			defer test.Done()
			ticker := time.NewTicker(tickOnEvery)
			defer ticker.Stop()
			for i := uint(0); i < calls; i++ {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}

				req, err := http.NewRequest("GET", targetURL, nil)
				if err != nil {
					t.logger.WithError(err).Error("Failed to prepare request")
					return
				}
				req = req.WithContext(ctx)

				resp, err := t.client.Do(req)
				if err != nil {
					aggr.Report(err, "Failed to do request")
					continue
				}

				b, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					aggr.Report(err, "Failed read resp body")
				} else if string(b) == "" {
					aggr.Report(err, fmt.Sprintf(
						"Empty response. ProxyError. KedgeErr(%s): %s, WinchErr(%s): %s",
						resp.Header.Get(header.ResponseKedgeErrorType),
						resp.Header.Get(header.ResponseKedgeError),
						resp.Header.Get(header.ResponseWinchErrorType),
						resp.Header.Get(header.ResponseWinchError),
					))
				} else if string(b) != expectedRes {
					aggr.Report(nil, fmt.Sprintf("Wrong response. Expected '%s', got %s", expectedRes, string(b)))
				}

				err = resp.Body.Close()
				if err != nil {
					aggr.Report(err, "Failed close resp body")
				}
			}
		}()
	}

	return test, aggr
}

type errAggregator struct {
	sync.Mutex
	m map[string]uint

	logger logrus.FieldLogger
}

func (a *errAggregator) Report(err error, msg string) {
	a.Lock()
	defer a.Unlock()

	a.m[fmt.Sprintf("%s: %v", msg, err)]++
	a.logger.WithError(err).Debug(msg)
}

func (a *errAggregator) Print() {
	if len(a.m) == 0 {
		return
	}

	msg := ""
	for err, num := range a.m {
		msg += fmt.Sprintf("%s = %v\n", err, num)
	}
	a.logger.Error(msg)
}
