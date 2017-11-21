package winch

import (
	"bytes"
	"net/http"
	"text/template"
	"time"

	"github.com/improbable-eng/go-httpwares/tags"
	"github.com/improbable-eng/kedge/pkg/sharedflags"
)

var (
	// TODO(bplotka): Consider another default, autodeducted from routing mapper (complex)
	flagShExpressions = sharedflags.Set.StringSlice("pac_redirect_sh_expressions", []string{},
		"Comma delimited array of shExpMatch expressions for host in the PAC. They will influence on what host"+
			" browser will redirect to winch. If empty it will redirect everything via winch.")
)

func NewPacFromFlags(winchHostPort string) (*Pac, error) {
	pac, err := generatePAC(winchHostPort, *flagShExpressions)
	if err != nil {
		return nil, err
	}
	p := &Pac{
		PAC:     pac,
		modTime: time.Now(),
	}
	return p, nil
}

// Pac is a handler that serves auto generated PAC file based on mapping routes.
type Pac struct {
	PAC     []byte
	modTime time.Time
}

func (p *Pac) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	// TODO(bplotka): Pass only local connections.

	tags := http_ctxtags.ExtractInbound(req)
	tags.Set(http_ctxtags.TagForCallService, "PAC")

	resp.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
	http.ServeContent(resp, req, "wpad.dat", p.modTime, bytes.NewReader(p.PAC)) // or proxy.pac
	return
}

var (
	pacTemplate = `function FindProxyForURL(url, host) {
	var proxy = "PROXY {{.WinchHostPort}}; DIRECT";
	var direct = "DIRECT";

	// no proxy for local hosts without domain:
	if(isPlainHostName(host)) return direct;

	// We only proxy http, not even https.
	if (
		url.substring(0, 4) == "ftp:" ||
		url.substring(0, 6) == "rsync:" ||
		url.substring(0, 6) == "https:"
	)
		return direct;

	// Commented for debug purposes.
	// Use direct connection whenever we have direct network connectivity.
	//if (isResolvable(host)) {
	//    return direct
	//}
	{{- if .Routes }}
	{{- range .Routes}}
	if (shExpMatch(host, "{{ . }}")) {
		return proxy;
	}
	{{- end}}

	return direct;
	{{- else }}
	return proxy;
	{{- end }}
}`
)

func generatePAC(winchHostPort string, rules []string) ([]byte, error) {
	tmpl, err := template.New("PAC").Parse(pacTemplate)
	if err != nil {
		return nil, err
	}

	buf := &bytes.Buffer{}
	err = tmpl.Execute(buf, struct {
		WinchHostPort string
		Routes        []string
	}{
		WinchHostPort: winchHostPort,
		Routes:        rules,
	})
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
