// Copyright (c) Improbable Worlds Ltd, All Rights Reserved

package main

import (
	"context"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/mwitkow/go-httpwares/metrics"
	"github.com/mwitkow/kedge/lib/http/header"
	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

var (
	flagLogLevel = sharedflags.Set.String("log_level", "debug", "Log level")
	flagScenario = sharedflags.Set.String("scenario_yaml", "", "Required flag. Scenario yaml content describing the test.")

	flagWinchURL = sharedflags.Set.String("winch_url", "http://127.0.0.1:8070", "If specified, "+
		"provided winch URL will be used on every load testing call. Currently the only supported load test flow is via winch.")
)

func main() {
	if err := sharedflags.Set.Parse(os.Args); err != nil {
		logrus.WithError(err).Fatal("failed parsing flags")
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
	logrus.Infof("Performing %s", *flagScenario)
	// Perform Load Test.
	t := newLoadTesterThroughWinch(logrus.StandardLogger(), *flagWinchURL)
	t.loadTest(context.Background(), s)
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

type scenario struct {
	TargetURL         string        `yaml:"target_url"`
	TestDuration      time.Duration `yaml:"duration"`
	TickOnEvery       time.Duration `yaml:"tick_on"`
	ConcurrentWorkers []uint        `yaml:"workers"`
	CallsPerWorker    uint          `yaml:"calls"`
	ExpectedRes       string        `yaml:"expected_response"`
}

func (t *tester) loadTest(ctx context.Context, sc scenario) {
	for _, workers := range sc.ConcurrentWorkers {
		for i := uint(0); i < sc.Repetitions; i++ {
			start, test := t.scheduleRequests(ctx, sc.TargetURL, workers, sc.CallsPerWorker, sc.ExpectedRes)
			t.logger.Infof("Starting %v concurrent requests against target %s [%v/%v]", workers, sc.TargetURL, i, sc.Repetitions)
			start.Done()
			test.Wait()
			t.logger.Info("Test Done")

			time.Sleep(sc.Pause)
		}

		// Print metrics
		printStats()
	}
}

func (t *tester) scheduleRequests(ctx context.Context, targetURL string, workers uint, calls uint, expectedRes string) (*sync.WaitGroup, *sync.WaitGroup) {
	start := &sync.WaitGroup{}
	start.Add(1)

	test := &sync.WaitGroup{}
	for i := uint(0); i < workers; i++ {
		test.Add(1)
		go func() {
			defer test.Done()
			for i := uint(0); i < calls; i++ {
				req, err := http.NewRequest("GET", targetURL, nil)
				if err != nil {
					t.logger.WithError(err).Error("Failed to prepare request")
					return
				}
				req = req.WithContext(ctx)

				// Wait for simultaneous (kind of) start (only first one)
				start.Wait()

				resp, err := t.client.Do(req)
				if err != nil {
					// too spammy?
					t.logger.WithError(err).Debug("Failed to do request")
					return
				}

				b, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					t.logger.WithError(err).Debug("Failed read resp body")
				} else if string(b) == "" {
					t.logger.WithFields(parseKedgeAndWinchResp(resp)).
						Debugf(
							"Empty response. ProxyError. KedgeErr: %s, WinchErr: %s",
							resp.Header.Get(header.ResponseKedgeError),
							resp.Header.Get(header.ResponseWinchError),
						)
				} else if string(b) != expectedRes {
					t.logger.Debugf("Wrong response. Expected '%s', got %s", expectedRes, string(b))
				}

				err = resp.Body.Close()
				if err != nil {
					t.logger.WithError(err).Debug("Failed close resp body")
					return
				}
			}
		}()
	}

	return start, test
}

func parseKedgeAndWinchResp(r *http.Response) map[string]interface{} {
	m := map[string]interface{}{}
	if val := r.Header.Get(header.ResponseKedgeErrorType); val != "" {
		m[header.ResponseKedgeErrorType] = val
	}
	if val := r.Header.Get(header.ResponseWinchErrorType); val != "" {
		m[header.ResponseWinchErrorType] = val
	}
	return m
}
