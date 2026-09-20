package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/midagedev/toktape/internal/config"
	"github.com/midagedev/toktape/internal/publish"
)

// Which service a command talks to, and with which token (track selfhost).
//
// Every verb that reaches the hub goes through resolveService, and no
// publish.Client is built anywhere else. Before this file each verb built its
// own client with `BaseURL: *url, Token: cfg.Token`, so a --url pointing
// anywhere carried the hosted service's journal token to that host in an
// Authorization header (measured 2026-09-20, cmd/toktape/service_test.go
// FAIL-first). The rule that closes the class rather than the symptom:
//
//	The token in config.toml belongs to the service config.toml names —
//	config `service`, or the hosted one when that key is unset — and is
//	sent to no other service. A token typed on the command line (--token,
//	TOKTAPE_TOKEN) is consent and travels wherever the URL points.
//
// A self-hoster names their hub once with `service` in config.toml and every
// verb follows; see `toktape help service` and web/README.md for the other
// half, standing the service up.

// serviceChoice is the one decision every publish-reading verb makes: where
// the request goes, which token it may carry, and where the URL came from.
type serviceChoice struct {
	// BaseURL is the service, normalised: lowercase scheme and host, no
	// trailing slash, an explicit default port dropped (https://h:443 is
	// https://h). Never empty.
	BaseURL string
	// Token is the credential this request may carry, "" for none.
	Token string
	// TokenWithheld says the config holds a journal token that stayed home
	// because the resolved service is not the one it belongs to. The command
	// that would have used it prints the one-line note (sayWithheldToken).
	TokenWithheld bool
	// Source names where BaseURL came from, one of the src* constants — for
	// the note, the hints and the tests.
	Source string
}

// Where a serviceChoice's URL came from, most specific first.
const (
	srcFlag    = "the --url flag"
	srcEnv     = "TOKTAPE_SERVICE"
	srcConfig  = "the service key in config.toml"
	srcDefault = "the default service"
)

// resolveService picks the service and the token for one command.
//
// URL precedence: the --url flag, then TOKTAPE_SERVICE, then the config's
// `service` key, then the hosted service. The token rule is the one in this
// file's doc block. flagToken is the --token flag; TOKTAPE_TOKEN is its equal
// and is read through getenv, which the tests replace.
func resolveService(flagURL, flagToken string, cfg *config.Config, getenv func(string) string) (serviceChoice, error) {
	// The URL: the first of the four that is set, each validated where it is
	// chosen so a bad value is refused with its origin named.
	var svc serviceChoice
	switch {
	case flagURL != "":
		svc.Source = srcFlag
	case getenv("TOKTAPE_SERVICE") != "":
		flagURL, svc.Source = getenv("TOKTAPE_SERVICE"), srcEnv
	case cfg != nil && cfg.Service != "":
		flagURL, svc.Source = cfg.Service, srcConfig
	default:
		flagURL, svc.Source = publish.DefaultBaseURL, srcDefault
	}
	base, err := parseServiceURL(flagURL)
	if err != nil {
		return svc, fmt.Errorf("%s: %v", svc.Source, err)
	}
	svc.BaseURL = base.String()

	// The token: consent first, then ownership.
	switch {
	case flagToken != "", getenv("TOKTAPE_TOKEN") != "":
		svc.Token = firstSet(flagToken, getenv("TOKTAPE_TOKEN"))
	case cfg != nil && cfg.Token != "":
		// The config's token follows the config's service, which is the
		// default when the key is unset. A service key that will not parse
		// is refused here rather than quietly withholding: the file holds a
		// credential whose destination cannot be stated, and that is a fact
		// to fix, not one to work around.
		homeBase, err := parseServiceURL(firstSet(cfg.Service, publish.DefaultBaseURL))
		if err != nil {
			return svc, fmt.Errorf("%s: %v", srcConfig, err)
		}
		if sameService(svc.BaseURL, homeBase.String()) {
			svc.Token = cfg.Token
		} else {
			svc.TokenWithheld = true
		}
	}
	return svc, nil
}

// sayWithheldToken prints the one stderr note a withheld token owes: the
// command ran without the credential it might have carried, and a reader who
// meant it to travel now knows which of the two knobs to turn.
func sayWithheldToken(c *cli, cfg *config.Config, svc serviceChoice) {
	if !svc.TokenWithheld {
		return
	}
	fmt.Fprintf(c.stderr, "note: the journal token in config.toml belongs to %s; not sent to %s. Pass --token, or set service in config.toml.\n",
		configServiceURL(cfg), svc.BaseURL)
}

// configServiceURL is the service the config names, normalised for display —
// the hosted one when the key is unset, which is what "the token belongs to"
// means on a machine that never said.
func configServiceURL(cfg *config.Config) string {
	raw := firstSet(serviceKey(cfg), publish.DefaultBaseURL)
	if s, err := parseServiceURL(raw); err == nil {
		return s.String()
	}
	return raw
}

func serviceKey(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Service
}

// serviceURL is a parsed service address: its origin, which is all the token
// rule compares, and any path a service mounted under a prefix keeps.
type serviceURL struct {
	origin string // scheme://host[:port], host lowercase, default port dropped
	path   string // "" or "/prefix"
}

func (s serviceURL) String() string { return s.origin + s.path }

// parseServiceURL normalises one service address.
//
//   - scheme must be http or https; anything else is refused naming the value
//   - plain http is allowed only for loopback hosts (localhost, 127.0.0.0/8,
//     ::1): elsewhere the token would travel in clear text, and that is said
//   - the host is lowercased and an explicit default port dropped, so
//     HTTPS://Host:443/ and https://host are the same service
//   - a trailing slash goes; credentials, a query or a fragment are refused —
//     each is a pasted URL that is not a service address
func parseServiceURL(raw string) (serviceURL, error) {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(s)
	if err != nil {
		return serviceURL{}, fmt.Errorf("%q is not a service address: %v", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return serviceURL{}, fmt.Errorf("%q is not a service address: it has to be http:// or https://", raw)
	}
	if u.Hostname() == "" {
		return serviceURL{}, fmt.Errorf("%q is not a service address: it names no host", raw)
	}
	if u.User != nil {
		return serviceURL{}, fmt.Errorf("%q is not a service address: it carries a user:password", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return serviceURL{}, fmt.Errorf("%q is not a service address: it carries a query or a fragment", raw)
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if u.Scheme == "http" && !isLoopback(host) {
		return serviceURL{}, fmt.Errorf("%q is plain http off localhost; the token would travel to it in clear text — use https", raw)
	}
	// Brackets back around an IPv6 literal: u.Hostname() strips them.
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	origin := u.Scheme + "://" + host
	if port != "" && !(u.Scheme == "https" && port == "443") && !(u.Scheme == "http" && port == "80") {
		origin += ":" + port
	}
	return serviceURL{origin: origin, path: u.Path}, nil
}

// sameService says whether two addresses are the same service: their origins
// (scheme, host, port — with default ports folded away) match. A path under
// which a service is mounted does not distinguish one service from another
// for the token rule; anything that will not parse is simply not the same.
func sameService(a, b string) bool {
	pa, errA := parseServiceURL(a)
	pb, errB := parseServiceURL(b)
	return errA == nil && errB == nil && pa.origin == pb.origin
}

// isLoopback covers localhost by name and every loopback address, including
// all of 127.0.0.0/8 and ::1 — a dev server on 127.0.0.7 is as local as one
// on 127.0.0.1.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// runLinkBase is the service inside a /r/<id> link, "" for a bare id. Used by
// publish --delete: a pasted link names the host the run lives on, and that
// is more specific than any resident setting.
func runLinkBase(arg string) string {
	i := strings.LastIndex(arg, "/r/")
	if i <= 0 || !strings.Contains(arg[:i], "://") {
		return ""
	}
	return arg[:i]
}
