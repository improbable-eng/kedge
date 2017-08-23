package oauth2auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mwitkow/kedge/lib/tokenauth"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"k8s.io/apimachinery/pkg/util/yaml"
	cfg "k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/jsonpath"
)

// Config represents Oauth2 config with additional CLI configuration to get access token.
// Compliant with k8s config.
type Config struct {
	CmdPath string `json:"cmd-path,omitempty"`
	CmdArgs string `json:"cmd-args,omitempty"`

	TokenKey    string `json:"token-key,omitempty"`
	ExpiryKey   string `json:"expiry-key,omitempty"`
	TimeFmt     string `json:"time-fmt,omitempty"`
	AccessToken string `json:"access-token,omitempty"`
	Expiry      string `json:"expiry,omitempty"`
}

func (c Config) toMap() (map[string]string, error) {
	config := c
	m, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	r := make(map[string]string)
	err = json.Unmarshal(m, &r)
	return r, err
}

// NewConfigFromMap constructs Oauth2 config but from generic map[string]string. Useful when used with kube config.
func NewConfigFromMap(m map[string]string) (Config, error) {
	marshaled, err := json.Marshal(m)
	if err != nil {
		return Config{}, err
	}

	r := Config{}
	err = json.Unmarshal(marshaled, &r)
	return r, err

}

// NewGCP constructs Oauth2 for Google provider tokenauth.Source which returns valid access tokens.
// Since saving and loading config is complainant with kube/config you are free to reuse that by putting kube config path inside
// `configPath` variable. This config file will be also used for caching valid tokens.
func NewGCP(name string, userName string, configPath string, initialConfig Config) (tokenauth.Source, error) {
	var err error
	var ts oauth2.TokenSource
	// Use CLI?
	if cmd := initialConfig.CmdPath; cmd != "" {
		ts = newCmdTokenSource(initialConfig)
	} else {
		// Otherwise use provider directly
		ts, err = google.DefaultTokenSource(context.Background(), "https://www.googleapis.com/auth/cloud-platform")
	}
	if err != nil {
		return nil, err
	}

	cache := &authCache{
		userName:   userName,
		configPath: configPath,
		name:       "gcp",
	}
	cts, err := newCachedTokenSource(initialConfig, cache, ts)
	if err != nil {
		return nil, err
	}

	return New(name, cts), nil
}

// AuthCache allows to save configuration info for just itself.
type AuthCache interface {
	Save(Config) error
}

// Complainant with kube config.
type authCache struct {
	configPath string
	userName   string
	name       string
}

func (c *authCache) Save(config Config) error {
	configContent, err := cfg.LoadFromFile(c.configPath)
	if err != nil {
		return errors.Wrapf(err, "Failed to load config from file %v. Make sure it is there or change"+
			" permissions.", c.configPath)
	}

	if configContent.AuthInfos == nil {
		configContent.AuthInfos = make(map[string]*api.AuthInfo)
	}

	if _, ok := configContent.AuthInfos[c.userName]; !ok {
		configContent.AuthInfos[c.userName] = &api.AuthInfo{
			AuthProvider: &api.AuthProviderConfig{
				Name: c.name,
			},
		}
	}

	oauth2Content, err := config.toMap()
	if err != nil {
		return err
	}

	configContent.AuthInfos[c.userName].AuthProvider.Config = oauth2Content
	return cfg.WriteToFile(*configContent, c.configPath)
}

type cachedTokenSource struct {
	mu          sync.Mutex
	source      oauth2.TokenSource
	accessToken string
	expiry      time.Time
	cache       AuthCache
	config      Config
}

func newCachedTokenSource(config Config, cache AuthCache, ts oauth2.TokenSource) (*cachedTokenSource, error) {
	var expiryTime time.Time
	if parsedTime, err := time.Parse(time.RFC3339Nano, config.Expiry); err == nil {
		expiryTime = parsedTime
	}
	return &cachedTokenSource{
		source:      ts,
		accessToken: config.AccessToken,
		expiry:      expiryTime,
		config:      config,
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
	t.config = t.update(tok)
	if t.cache != nil {
		if err := t.cache.Save(t.config); err != nil {
			fmt.Printf("Warn: Failed to save token: %v", err)
		}
	}
	return tok, nil
}

func (t *cachedTokenSource) cachedToken() *oauth2.Token {
	t.mu.Lock()
	defer t.mu.Unlock()
	return &oauth2.Token{
		AccessToken: t.accessToken,
		TokenType:   "Bearer",
		Expiry:      t.expiry,
	}
}

func (t *cachedTokenSource) update(tok *oauth2.Token) Config {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.accessToken = tok.AccessToken
	t.expiry = tok.Expiry

	cloned := t.config
	cloned.AccessToken = t.accessToken
	cloned.Expiry = t.expiry.Format(time.RFC3339Nano)
	return cloned
}

type commandTokenSource struct {
	cmd       string
	args      []string
	tokenKey  string
	expiryKey string
	timeFmt   string
}

func newCmdTokenSource(config Config) *commandTokenSource {
	timeFmt := config.TimeFmt
	if len(timeFmt) == 0 {
		timeFmt = time.RFC3339Nano
	}
	tokenKey := config.TokenKey
	if len(tokenKey) == 0 {
		tokenKey = "{.access_token}"
	}
	expiryKey := config.ExpiryKey
	if len(expiryKey) == 0 {
		expiryKey = "{.token_expiry}"
	}
	cmd := config.CmdPath
	var args []string
	if cmdArgs := config.CmdArgs; cmdArgs != "" {
		args = strings.Fields(cmdArgs)
	} else {
		fields := strings.Fields(cmd)
		cmd = fields[0]
		args = fields[1:]
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
		return nil, errors.Wrapf(err, "error executing access token command %q: output=%s", fullCmd, output)
	}
	token, err := c.parseTokenCmdOutput(output)
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing output for access token command %q", fullCmd)
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
		return nil, errors.Wrapf(err, "error parsing token-key %q from %q", c.tokenKey, string(output))
	}
	expiryStr, err := parseJSONPath(data, "expiry-key", c.expiryKey)
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing expiry-key %q from %q", c.expiryKey, string(output))
	}
	var expiry time.Time
	if t, err := time.Parse(c.timeFmt, expiryStr); err != nil {
		fmt.Printf("Warn: Failed to parse token expiry from %s (fmt=%s): %v", expiryStr, c.timeFmt, err)
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
