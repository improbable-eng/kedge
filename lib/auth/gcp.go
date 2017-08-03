package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"k8s.io/apimachinery/pkg/util/yaml"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/util/jsonpath"
	"k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
)

// Mostly copied from k8s.io/client-go/plugin/pkg/client/auth/gcp/gcp.go file.

// gcpAuthSource is an auth provider plugin that uses GCP credentials to provide
// tokens.
type gcpAuthSource struct {
	name        string
	tokenSource oauth2.TokenSource
	persister   restclient.AuthProviderConfigPersister
}

func gcp(name string, userName string, configPath string, gcpConfig map[string]string) (Source, error) {
	var ts oauth2.TokenSource
	var err error
	if cmd, useCmd := gcpConfig["cmd-path"]; useCmd {
		if len(cmd) == 0 {
			return nil, fmt.Errorf("missing access token cmd")
		}
		var args []string
		if cmdArgs, ok := gcpConfig["cmd-args"]; ok {
			args = strings.Fields(cmdArgs)
		} else {
			fields := strings.Fields(cmd)
			cmd = fields[0]
			args = fields[1:]
		}
		ts = newCmdTokenSource(cmd, args, gcpConfig["token-key"], gcpConfig["expiry-key"], gcpConfig["time-fmt"])
	} else {
		ts, err = google.DefaultTokenSource(context.Background(), "https://www.googleapis.com/auth/cloud-platform")
	}
	if err != nil {
		return nil, err
	}

	persister := clientcmd.PersisterForUser(
		&clientcmd.ClientConfigLoadingRules{Precedence: []string{configPath}},
		userName,
	)
	cts, err := newCachedTokenSource(gcpConfig["access-token"], gcpConfig["expiry"], persister, ts, gcpConfig)
	if err != nil {
		return nil, err
	}

	return &gcpAuthSource{
		name:        name,
		tokenSource: cts,
		persister:   persister,
	}, nil
}

func (s *gcpAuthSource) Name() string {
	return s.name
}

func (s *gcpAuthSource) HeaderValue() (string, error) {
	token, err := s.tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("failed to obtain ID Token. Err: %v", err)
	}

	return fmt.Sprintf("Bearer %s", token.AccessToken), nil
}

type cachedTokenSource struct {
	lk          sync.Mutex
	source      oauth2.TokenSource
	accessToken string
	expiry      time.Time
	persister   restclient.AuthProviderConfigPersister
	cache       map[string]string
}

func newCachedTokenSource(accessToken, expiry string, persister restclient.AuthProviderConfigPersister, ts oauth2.TokenSource, cache map[string]string) (*cachedTokenSource, error) {
	var expiryTime time.Time
	if parsedTime, err := time.Parse(time.RFC3339Nano, expiry); err == nil {
		expiryTime = parsedTime
	}
	if cache == nil {
		cache = make(map[string]string)
	}
	return &cachedTokenSource{
		source:      ts,
		accessToken: accessToken,
		expiry:      expiryTime,
		persister:   persister,
		cache:       cache,
	}, nil
}

func (t *cachedTokenSource) Token() (*oauth2.Token, error) {
	tok := t.cachedToken()
	if tok.Valid() && !tok.Expiry.IsZero() {
		return tok, nil
	}
	tok, err := t.source.Token()
	if err != nil {
		return nil, err
	}
	cache := t.update(tok)
	if t.persister != nil {
		if err := t.persister.Persist(cache); err != nil {
			glog.V(4).Infof("Failed to persist token: %v", err)
		}
	}
	return tok, nil
}

func (t *cachedTokenSource) cachedToken() *oauth2.Token {
	t.lk.Lock()
	defer t.lk.Unlock()
	return &oauth2.Token{
		AccessToken: t.accessToken,
		TokenType:   "Bearer",
		Expiry:      t.expiry,
	}
}

func (t *cachedTokenSource) update(tok *oauth2.Token) map[string]string {
	t.lk.Lock()
	defer t.lk.Unlock()
	t.accessToken = tok.AccessToken
	t.expiry = tok.Expiry
	ret := map[string]string{}
	for k, v := range t.cache {
		ret[k] = v
	}
	ret["access-token"] = t.accessToken
	ret["expiry"] = t.expiry.Format(time.RFC3339Nano)
	return ret
}

type commandTokenSource struct {
	cmd       string
	args      []string
	tokenKey  string
	expiryKey string
	timeFmt   string
}

func newCmdTokenSource(cmd string, args []string, tokenKey, expiryKey, timeFmt string) *commandTokenSource {
	if len(timeFmt) == 0 {
		timeFmt = time.RFC3339Nano
	}
	if len(tokenKey) == 0 {
		tokenKey = "{.access_token}"
	}
	if len(expiryKey) == 0 {
		expiryKey = "{.token_expiry}"
	}
	return &commandTokenSource{
		cmd:       cmd,
		args:      args,
		tokenKey:  tokenKey,
		expiryKey: expiryKey,
		timeFmt:   timeFmt,
	}
}

func (c *commandTokenSource) Token() (*oauth2.Token, error) {
	fullCmd := strings.Join(append([]string{c.cmd}, c.args...), " ")
	cmd := exec.Command(c.cmd, c.args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error executing access token command %q: err=%v output=%s", fullCmd, err, output)
	}
	token, err := c.parseTokenCmdOutput(output)
	if err != nil {
		return nil, fmt.Errorf("error parsing output for access token command %q: %v", fullCmd, err)
	}
	return token, nil
}

func (c *commandTokenSource) parseTokenCmdOutput(output []byte) (*oauth2.Token, error) {
	output, err := yaml.ToJSON(output)
	if err != nil {
		return nil, err
	}
	var data interface{}
	if err := json.Unmarshal(output, &data); err != nil {
		return nil, err
	}

	accessToken, err := parseJSONPath(data, "token-key", c.tokenKey)
	if err != nil {
		return nil, fmt.Errorf("error parsing token-key %q from %q: %v", c.tokenKey, string(output), err)
	}
	expiryStr, err := parseJSONPath(data, "expiry-key", c.expiryKey)
	if err != nil {
		return nil, fmt.Errorf("error parsing expiry-key %q from %q: %v", c.expiryKey, string(output), err)
	}
	var expiry time.Time
	if t, err := time.Parse(c.timeFmt, expiryStr); err != nil {
		glog.V(4).Infof("Failed to parse token expiry from %s (fmt=%s): %v", expiryStr, c.timeFmt, err)
	} else {
		expiry = t
	}

	return &oauth2.Token{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		Expiry:      expiry,
	}, nil
}

func parseJSONPath(input interface{}, name, template string) (string, error) {
	j := jsonpath.New(name)
	buf := new(bytes.Buffer)
	if err := j.Parse(template); err != nil {
		return "", err
	}
	if err := j.Execute(buf, input); err != nil {
		return "", err
	}
	return buf.String(), nil
}

type conditionalTransport struct {
	oauthTransport *oauth2.Transport
	persister      restclient.AuthProviderConfigPersister
}

func (t *conditionalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(req.Header.Get("Authorization")) != 0 {
		return t.oauthTransport.Base.RoundTrip(req)
	}

	res, err := t.oauthTransport.RoundTrip(req)

	if err != nil {
		return nil, err
	}

	if res.StatusCode == 401 {
		glog.V(4).Infof("The credentials that were supplied are invalid for the target cluster")
		emptyCache := make(map[string]string)
		t.persister.Persist(emptyCache)
	}

	return res, nil
}
