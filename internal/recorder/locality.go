package recorder

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// Where the server runs (TTP-196, 2026-10-12).
//
// `toktape decide --url http://ws:8091` from a Mac recorded `Apple M1 Pro /
// macos` as the rig: the host line (procmon) and the GPU inventory and
// sampler (nvidia-smi) are this machine's, and nothing asked whether the
// server was. serverIsLocal is the one place that question is answered.
// collectHost and collectDecisionHost call it; the GPU sampler needs no
// second call, because it reads the collector collectHost leaves in r.gpus.
// Nothing else re-derives it. (loopbackPortOf, which finds a pid by port, asks
// the narrower question "does this port name a process in this /proc" and
// shares only the loopback host test below.)

// machineView is what the recorder may ask about the machine it runs on. It is
// a package var so a test answers for a machine that is not the one it runs
// on and never touches the network.
type machineView struct {
	hostname   func() (string, error)
	ifaceAddrs func() ([]net.Addr, error)
	lookupHost func(ctx context.Context, host string) ([]string, error)
}

var thisMachine = machineView{
	hostname:   os.Hostname,
	ifaceAddrs: net.InterfaceAddrs,
	lookupHost: net.DefaultResolver.LookupHost,
}

// resolveBudget bounds the one name lookup serverIsLocal may make. A name that
// does not answer in this time is remote: a hung resolver must not hold up a
// run that has not started.
var resolveBudget = time.Second

// isLoopbackHost reports whether h, a URL hostname (no brackets), is a
// loopback name or address: localhost in any case, and anything in
// 127.0.0.0/8 or ::1. It is the one host test for "this machine by the
// loopback" — loopbackPortOf and serverIsLocal both call it, so the pid search
// and the host line cannot disagree about what loopback is.
func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	a, err := netip.ParseAddr(h)
	return err == nil && a.Unmap().IsLoopback()
}

// serverIsLocal says whether the server at baseURL runs on this machine, and
// when it does not, returns the one warning that goes into the tape. local is
// true only when a connection to baseURL lands here:
//
//   - a loopback host (isLoopbackHost), or an unspecified address
//     (`http://0.0.0.0:8080`, which is what llama-server prints for
//     --host 0.0.0.0 and users paste back);
//   - an IP literal on one of this machine's interfaces;
//   - this machine's own hostname, compared whole and without case. The first
//     label of a longer name is not a match: a false "local" records this
//     machine as someone else's rig, and a false "remote" prints `?`;
//   - a name that resolves, within resolveBudget, to addresses ALL of which
//     are this machine's. A name that points at this machine and another one
//     is not claimed.
//
// Everything else is remote, including a name that does not resolve or does
// not resolve in time. An engine's declared pid is not evidence: a bloomery on
// another host declares its own pid over HTTP.
func serverIsLocal(ctx context.Context, baseURL string) (local bool, warning string) {
	var host string
	if u, err := url.Parse(server.NormalizeBaseURL(baseURL)); err == nil {
		host = u.Hostname()
	}
	if host == "" {
		return false, remoteHostWarning("its address could not be read")
	}
	if isLoopbackHost(host) {
		return true, ""
	}
	locals := interfaceAddrs()
	if a, err := netip.ParseAddr(host); err == nil {
		if isThisMachine(a, locals) {
			return true, ""
		}
		return false, remoteHostWarning("")
	}
	name := strings.TrimSuffix(host, ".")
	if own, err := thisMachine.hostname(); err == nil && strings.EqualFold(strings.TrimSuffix(own, "."), name) {
		return true, ""
	}
	addrs, err := lookupBounded(ctx, name)
	if err != nil || len(addrs) == 0 {
		why := "its name did not resolve"
		if errors.Is(err, context.DeadlineExceeded) {
			why = "its name did not resolve in time"
		}
		return false, remoteHostWarning(why)
	}
	for _, s := range addrs {
		a, err := netip.ParseAddr(s)
		if err != nil || !isThisMachine(a, locals) {
			return false, remoteHostWarning("")
		}
	}
	return true, ""
}

// remoteHostWarning is the one sentence a run against another host adds in
// place of the host line. why says what made the answer "remote" when the
// reader could not otherwise tell it from a name that resolved elsewhere.
//
// It does not name the server: a card prints warnings verbatim and gets
// posted, `--host-label` exists to keep a place name out of the bytes (TTP-93),
// and the URL is already in summary.server.url for anyone who needs it.
func remoteHostWarning(why string) string {
	if why != "" {
		why = " (" + why + ")"
	}
	return "server is not on this machine" + why + "; host figures unknown"
}

// unknownHost is the host line of a server that is not on this machine: no
// procmon read, no GPU inventory, nothing printed that this machine could
// answer for. What the operator stated still applies, because a stated value
// is the operator's observation: --host-label names the server's place and
// the RAM options give its memory figures. Both go through the same apply
// calls the local path uses, in the same order.
func unknownHost(ram HostRAM, label HostLabel) tape.HostInfo {
	var host tape.HostInfo
	ram.apply(&host)
	label.apply(&host)
	return host
}

// lookupBounded resolves name through thisMachine within resolveBudget. A
// resolver that ignores its context is left behind rather than waited for.
func lookupBounded(ctx context.Context, name string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, resolveBudget)
	defer cancel()
	type answer struct {
		addrs []string
		err   error
	}
	ch := make(chan answer, 1)
	lookup := thisMachine.lookupHost
	go func() {
		addrs, err := lookup(ctx, name)
		ch <- answer{addrs, err}
	}()
	select {
	case a := <-ch:
		return a.addrs, a.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// interfaceAddrs is the addresses on this machine's interfaces, unmapped and
// without zones. A machine whose interfaces cannot be listed recognises only
// loopback and its hostname, and says remote for the rest.
func interfaceAddrs() []netip.Addr {
	addrs, err := thisMachine.ifaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if n, ok := netip.AddrFromSlice(ip); ok {
			out = append(out, n.Unmap().WithZone(""))
		}
	}
	return out
}

// isThisMachine reports whether a connection to a lands on this machine:
// loopback, unspecified, or an address on one of its interfaces.
func isThisMachine(a netip.Addr, locals []netip.Addr) bool {
	a = a.Unmap().WithZone("")
	if a.IsLoopback() || a.IsUnspecified() {
		return true
	}
	for _, l := range locals {
		if a == l {
			return true
		}
	}
	return false
}
