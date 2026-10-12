package recorder

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// TTP-196 (2026-10-02, round 2026-10-12): `toktape decide --url http://ws:8091`
// from a Mac recorded `Apple M1 Pro / macos` as the rig, because the host line
// and the GPU inventory always read THIS machine. These tests pin the one
// owner of "the server runs on this host" (serverIsLocal) and what each
// caller does with its answer.

// localityRoot is a fixture /proc that reads rig-epyc / AMD EPYC 7313 /
// 6.8.0-45-generic: a host that is distinguishable from any real machine a
// test could run on, so "was it read" is a search for those strings.
const localityRoot = "../procmon/testdata"

// fakeMachine installs a view of "this machine" for one test: its hostname,
// the addresses on its interfaces, and a resolver answering from dns (a name
// absent from it fails to resolve). The returned log is every name the
// resolver was asked, so a test can say a loopback URL never reached it.
type lookupLog struct {
	mu    sync.Mutex
	names []string
}

func (l *lookupLog) add(n string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.names = append(l.names, n)
}

func (l *lookupLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.names)
}

func fakeMachine(t *testing.T, hostname string, ifaces []string, dns map[string][]string) *lookupLog {
	t.Helper()
	log := &lookupLog{}
	var addrs []net.Addr
	for _, c := range ifaces {
		ip, ipnet, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatalf("bad interface %q: %v", c, err)
		}
		addrs = append(addrs, &net.IPNet{IP: ip, Mask: ipnet.Mask})
	}
	prev := thisMachine
	thisMachine = machineView{
		hostname:   func() (string, error) { return hostname, nil },
		ifaceAddrs: func() ([]net.Addr, error) { return addrs, nil },
		lookupHost: func(ctx context.Context, name string) ([]string, error) {
			log.add(name)
			if ans, ok := dns[name]; ok {
				return ans, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
		},
	}
	t.Cleanup(func() { thisMachine = prev })
	return log
}

// A desktop with one LAN address and one global v6 address.
var desktopIfaces = []string{"127.0.0.1/8", "192.168.0.12/24", "2001:db8::5/64"}

// realBackendGPU is the nvidia-smi backend over the fixture CSVs, with a
// counter of every query made after it opened. A real backend matters: on a
// Mac with no nvidia-smi gpu.Open already returns the Null collector, so only
// a collector that reads devices can show that a remote run did not ask it.
func realBackendGPU(t *testing.T) (gpu.Collector, *atomic.Int32) {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	cards, apps := read("nvidia-query-gpu.csv"), read("nvidia-compute-apps.csv")
	var calls atomic.Int32
	runner := func(ctx context.Context, args ...string) (string, error) {
		calls.Add(1)
		for _, a := range args {
			switch {
			case strings.HasPrefix(a, "--query-compute-apps="):
				return apps, nil
			case strings.HasPrefix(a, "--query-gpu="):
				return cards, nil
			}
		}
		return "", errors.New("unexpected nvidia-smi query")
	}
	c, warns := gpu.OpenWith(context.Background(), runner)
	if c.Name() != "nvidia-smi" {
		t.Fatalf("fake GPU backend is %q, warnings %v", c.Name(), warns)
	}
	t.Cleanup(c.Close)
	calls.Store(0)
	return c, &calls
}

// remoteWarning is the one sentence a remote run adds. Its wording is pinned
// loosely: the two clauses a reader needs, and nothing that names the server.
func isRemoteWarning(w string) bool {
	return strings.HasPrefix(w, "server is not on this machine") && strings.HasSuffix(w, "host figures unknown")
}

func TestServerIsLocal(t *testing.T) {
	dns := map[string][]string{
		"nas.lan":                  {"192.168.0.12"},
		"alias.lan":                {"127.0.1.1"},
		"dual.lan":                 {"192.168.0.12", "2001:db8::5"},
		"both.lan":                 {"192.168.0.12", "192.168.0.40"},
		"spark.example":            {"203.0.113.9"},
		"my-desktop.other.example": {"203.0.113.10"},
		"empty.lan":                {},
	}
	cases := []struct {
		name    string
		url     string
		local   bool
		lookups int
	}{
		// Loopback: the whole of 127.0.0.0/8, in any case, never resolved.
		{"127.0.0.1", "http://127.0.0.1:8080", true, 0},
		{"localhost", "http://localhost:8080", true, 0},
		{"LOCALHOST", "http://LOCALHOST:8080", true, 0},
		{"::1", "http://[::1]:8080", true, 0},
		{"127.0.0.2", "http://127.0.0.2:8080", true, 0},
		// The address llama-server prints for --host 0.0.0.0. A dial to it
		// reaches this machine, and users paste it.
		{"0.0.0.0", "http://0.0.0.0:8080", true, 0},
		{"[::]", "http://[::]:8080", true, 0},
		// This machine by its own name or address.
		{"own hostname", "http://my-desktop:8080", true, 0},
		{"own hostname, other case, root dot", "http://MY-DESKTOP.:8080", true, 0},
		{"own IPv4", "http://192.168.0.12:8080", true, 0},
		{"own IPv6", "http://[2001:db8::5]:8080", true, 0},
		{"no scheme, own IPv4", "192.168.0.12:8080", true, 0},
		// A name that resolves to this machine, every address of it.
		{"name resolving to own IPv4", "http://nas.lan:8080", true, 1},
		{"name resolving to loopback (127.0.1.1 in /etc/hosts)", "http://alias.lan:8080", true, 1},
		{"name resolving to own v4 and v6", "http://dual.lan:8080", true, 1},
		// Remote.
		{"foreign IPv4", "http://192.168.0.40:8080", false, 0},
		{"foreign name", "http://spark.example:8080", false, 1},
		{"no scheme, foreign name", "spark.example:8080", false, 1},
		{"name resolving to own and another machine", "http://both.lan:8080", false, 1},
		{"own first label is not a match", "http://my-desktop.other.example:8080", false, 1},
		{"name that resolves to nothing", "http://empty.lan:8080", false, 1},
		{"unparseable", "http://[::1", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := fakeMachine(t, "my-desktop", desktopIfaces, dns)
			local, warning := serverIsLocal(context.Background(), tc.url)
			if local != tc.local {
				t.Errorf("serverIsLocal(%q) = %v, want %v (warning %q)", tc.url, local, tc.local, warning)
			}
			if tc.local && warning != "" {
				t.Errorf("a local server carries a warning: %q", warning)
			}
			if !tc.local && !isRemoteWarning(warning) {
				t.Errorf("a remote server's warning = %q, want one saying it is not on this machine and the host figures are unknown", warning)
			}
			if strings.Contains(warning, "\n") {
				t.Errorf("warning is not one line: %q", warning)
			}
			if got := log.count(); got != tc.lookups {
				t.Errorf("resolver asked %d times (%v), want %d", got, log.names, tc.lookups)
			}
		})
	}
}

// The host test is one function. loopbackPortOf (the pid-by-port search) and
// serverIsLocal both ask it, so they cannot disagree about what loopback is.
func TestLoopbackHostIsOneTest(t *testing.T) {
	for _, h := range []string{"127.0.0.1", "127.0.0.2", "127.255.255.254", "localhost", "LocalHost", "::1", "[::1]"} {
		if !isLoopbackHost(strings.Trim(h, "[]")) {
			t.Errorf("isLoopbackHost(%q) = false, want true", h)
		}
		host := h
		if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
			host = "[" + h + "]"
		}
		if port, err := loopbackPortOf("http://" + host + ":8080"); err != nil || port != 8080 {
			t.Errorf("loopbackPortOf(%s) = %d, %v; want 8080 from the same test", host, port, err)
		}
	}
	for _, h := range []string{"128.0.0.1", "10.0.0.1", "192.168.0.12", "spark.example", "localhost.example", "ws", ""} {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true, want false", h)
		}
		if _, err := loopbackPortOf("http://" + h + ":8080"); err == nil {
			t.Errorf("loopbackPortOf(%q) accepted a host that is not loopback", h)
		}
	}
}

// A resolver that fails is "remote", and the one warning says the name did
// not resolve, so a reader can tell it from a name that resolved elsewhere.
func TestResolverErrorIsRemoteAndSaysSo(t *testing.T) {
	fakeMachine(t, "my-desktop", desktopIfaces, nil) // every name fails
	local, w := serverIsLocal(context.Background(), "http://ws:8091")
	if local {
		t.Fatalf("a name that did not resolve was taken as this machine")
	}
	if !isRemoteWarning(w) || !strings.Contains(w, "did not resolve") {
		t.Errorf("warning = %q, want the remote sentence naming the failed resolve", w)
	}
	// A name that DID resolve elsewhere does not claim the resolve failed.
	fakeMachine(t, "my-desktop", desktopIfaces, map[string][]string{"ws": {"203.0.113.9"}})
	_, w = serverIsLocal(context.Background(), "http://ws:8091")
	if strings.Contains(w, "did not resolve") {
		t.Errorf("warning = %q, claims the resolve failed when it answered", w)
	}
}

// A resolver that never answers does not hang the run: one that honours its
// context ends at the budget, and one that ignores it is still left behind.
func TestResolverHangIsRemoteWithinTheBudget(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	cases := []struct {
		name   string
		budget time.Duration
		lookup func(ctx context.Context, host string) ([]string, error)
	}{
		{"honours ctx, the real budget", resolveBudget, func(ctx context.Context, host string) ([]string, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}},
		{"ignores ctx", 50 * time.Millisecond, func(ctx context.Context, host string) ([]string, error) {
			<-release
			return nil, errors.New("released")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeMachine(t, "my-desktop", desktopIfaces, nil)
			thisMachine.lookupHost = tc.lookup
			prev := resolveBudget
			resolveBudget = tc.budget
			t.Cleanup(func() { resolveBudget = prev })

			start := time.Now()
			local, w := serverIsLocal(context.Background(), "http://ws:8091")
			if d := time.Since(start); d >= 2*time.Second {
				t.Errorf("serverIsLocal took %v, want under 2s", d)
			}
			if local || !isRemoteWarning(w) || !strings.Contains(w, "did not resolve") {
				t.Errorf("local=%v warning=%q, want remote naming the failed resolve", local, w)
			}
		})
	}
}

func remoteRun(t *testing.T, url string, opts Options) *run {
	t.Helper()
	if opts.FSRoot == "" {
		opts.FSRoot = localityRoot
	}
	return &run{opts: opts, client: server.New(url)}
}

// Gate 1: a server on another host leaves the host line and the GPU view
// unknown. The fixture /proc and the real-backend GPU are both right there to
// be read, which is the defect.
func TestCollectHostRemoteIsUnknown(t *testing.T) {
	for _, withCollector := range []bool{true, false} {
		name := "no collector supplied (gpu.Open must not run)"
		if withCollector {
			name = "a collector supplied (it stands for this machine's cards)"
		}
		t.Run(name, func(t *testing.T) {
			fakeMachine(t, "my-desktop", desktopIfaces, map[string][]string{"spark.example": {"203.0.113.9"}})
			opts := Options{}
			var calls *atomic.Int32
			if withCollector {
				opts.GPU, calls = realBackendGPU(t)
			}
			r := remoteRun(t, "http://spark.example:8080", opts)
			r.collectHost(context.Background())

			h := r.host
			if h.CPU != "" || h.CPUCores != 0 || h.CPUThreads != 0 || h.RAMBytes != 0 || h.Kernel != "" || h.OS != "" {
				t.Errorf("a remote server's host line was read from this machine: %+v", h)
			}
			if h.Hostname != "" || h.HostnameSource != "" {
				t.Errorf("hostname %q (%q), want none: it names this machine, not the server", h.Hostname, h.HostnameSource)
			}
			if len(h.GPUs) != 0 {
				t.Errorf("host.GPUs = %v, want empty", h.GPUs)
			}
			if r.gpuViewTaken() {
				t.Errorf("gpuViewTaken() = true: this machine's GPUs were looked at for a server that is not here")
			}
			if r.ownGPU {
				t.Errorf("ownGPU = true on the Null collector: nothing for the run to close")
			}
			if calls != nil && calls.Load() != 0 {
				t.Errorf("the supplied collector was queried %d times", calls.Load())
			}
			if len(r.warnings) != 1 || !isRemoteWarning(r.warnings[0]) {
				t.Errorf("warnings = %q, want exactly the one remote sentence", r.warnings)
			}
			// The sampler reads r.gpus (record.go) and the placement replay
			// reads placementGPUs: neither may see a count for these cards.
			if n := r.placementGPUs(); n != -1 {
				t.Errorf("placementGPUs() = %d, want -1 (not looked)", n)
			}
		})
	}
}

// Gates 2 and 3: this machine by its own name, an address on its interfaces,
// or loopback reads the host exactly as before.
func TestCollectHostLocalIsReadAsBefore(t *testing.T) {
	want, err := procmon.HostInfo(localityRoot)
	if err != nil {
		t.Fatal(err)
	}
	want.HostnameSource = tape.HostnameObserved
	if want.CPU == "" || want.Kernel == "" || want.Hostname == "" {
		t.Fatalf("fixture host is not distinguishable: %+v", want)
	}
	for _, tc := range []struct {
		name, url string
		lookups   int
	}{
		{"own hostname (injected)", "http://my-desktop:8080", 0},
		{"own interface IP (injected)", "http://192.168.0.12:8080", 0},
		{"name resolving to own IP (injected)", "http://nas.lan:8080", 1},
		{"127.0.0.1", "http://127.0.0.1:8080", 0},
		{"localhost", "http://localhost:8080", 0},
		{"[::1]", "http://[::1]:8080", 0},
		{"127.0.0.2", "http://127.0.0.2:8080", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := fakeMachine(t, "my-desktop", desktopIfaces, map[string][]string{"nas.lan": {"192.168.0.12"}})
			c, calls := realBackendGPU(t)
			r := remoteRun(t, tc.url, Options{GPU: c})
			r.collectHost(context.Background())

			wantHost := want
			wantHost.GPUs = r.host.GPUs
			if !reflect.DeepEqual(r.host, wantHost) {
				t.Errorf("host = %+v\nwant %+v", r.host, wantHost)
			}
			if len(r.host.GPUs) == 0 || calls.Load() == 0 {
				t.Errorf("the GPU inventory was not read: %d devices, %d queries", len(r.host.GPUs), calls.Load())
			}
			if !r.gpuViewTaken() {
				t.Errorf("gpuViewTaken() = false on a local run with a real backend")
			}
			if len(r.warnings) != 0 {
				t.Errorf("warnings = %q, want none", r.warnings)
			}
			if got := log.count(); got != tc.lookups {
				t.Errorf("resolver asked %d times, want %d", got, tc.lookups)
			}
		})
	}
}

// Gate 4: what the operator stated still applies to a remote server, and
// nothing else does. A label is an operator's word about the server's place;
// the RAM figures are theirs to give.
func TestCollectHostRemoteKeepsWhatTheOperatorStated(t *testing.T) {
	fakeMachine(t, "my-desktop", desktopIfaces, map[string][]string{"spark.example": {"203.0.113.9"}})
	c, calls := realBackendGPU(t)
	r := remoteRun(t, "http://spark.example:8080", Options{
		GPU:       c,
		HostLabel: HostLabel{Set: true, Text: "dgx-spark"},
		HostRAM: HostRAM{
			BytesPerSec: 273_000_000_000, Speed: "LPDDR5X-8533", Channels: 8,
			Source: tape.RAMSourceStated,
		},
	})
	r.collectHost(context.Background())

	want := tape.HostInfo{
		Hostname: "dgx-spark", HostnameSource: tape.HostnameLabelled,
		RAMBytesPerSec: 273_000_000_000, RAMSpeed: "LPDDR5X-8533", RAMChannels: 8,
		RAMSource: tape.RAMSourceStated,
	}
	if !reflect.DeepEqual(r.host, want) {
		t.Errorf("host = %+v\nwant %+v", r.host, want)
	}
	if calls.Load() != 0 || r.gpuViewTaken() {
		t.Errorf("GPU looked at: %d queries, view taken %v", calls.Load(), r.gpuViewTaken())
	}
	if len(r.warnings) != 1 || !isRemoteWarning(r.warnings[0]) {
		t.Errorf("warnings = %q, want exactly the one remote sentence", r.warnings)
	}

	// The label alone: the RAM line stays unknown.
	r = remoteRun(t, "http://spark.example:8080", Options{HostLabel: HostLabel{Set: true, Text: ""}})
	r.collectHost(context.Background())
	if want := (tape.HostInfo{HostnameSource: tape.HostnameLabelled}); !reflect.DeepEqual(r.host, want) {
		t.Errorf("an empty label: host = %+v, want %+v", r.host, want)
	}
}

// A resolver that errors, through the caller that matters: the run finishes
// fast, remote, with the failed resolve named in its one warning.
func TestCollectHostResolverErrorFinishesFast(t *testing.T) {
	fakeMachine(t, "my-desktop", desktopIfaces, nil)
	r := remoteRun(t, "http://ws:8091", Options{})
	start := time.Now()
	r.collectHost(context.Background())
	if d := time.Since(start); d >= 2*time.Second {
		t.Errorf("collectHost took %v, want under 2s", d)
	}
	if r.host.CPU != "" || r.host.Kernel != "" || len(r.host.GPUs) != 0 {
		t.Errorf("host = %+v, want unknown", r.host)
	}
	if len(r.warnings) != 1 || !isRemoteWarning(r.warnings[0]) || !strings.Contains(r.warnings[0], "did not resolve") {
		t.Errorf("warnings = %q, want the one remote sentence naming the failed resolve", r.warnings)
	}
}

// Gate 5: decide reads the host the same way, through the same owner.
func TestCollectDecisionHost(t *testing.T) {
	dns := map[string][]string{"spark.example": {"203.0.113.9"}}
	t.Run("remote is unknown, the label applies", func(t *testing.T) {
		fakeMachine(t, "my-desktop", desktopIfaces, dns)
		c, calls := realBackendGPU(t)
		host, warns := collectDecisionHost(context.Background(), DecisionOptions{
			FSRoot: localityRoot, GPU: c,
			HostLabel: HostLabel{Set: true, Text: "dgx-spark"},
		}, "http://spark.example:8091")
		want := tape.HostInfo{Hostname: "dgx-spark", HostnameSource: tape.HostnameLabelled}
		if !reflect.DeepEqual(host, want) {
			t.Errorf("host = %+v, want %+v", host, want)
		}
		if calls.Load() != 0 {
			t.Errorf("the supplied collector was queried %d times", calls.Load())
		}
		if len(warns) != 1 || !isRemoteWarning(warns[0]) {
			t.Errorf("warnings = %q, want exactly the one remote sentence", warns)
		}
	})
	t.Run("remote with no collector supplied does not open one", func(t *testing.T) {
		fakeMachine(t, "my-desktop", desktopIfaces, dns)
		host, warns := collectDecisionHost(context.Background(), DecisionOptions{FSRoot: localityRoot}, "http://spark.example:8091")
		if !reflect.DeepEqual(host, tape.HostInfo{}) {
			t.Errorf("host = %+v, want empty", host)
		}
		if len(warns) != 1 || !isRemoteWarning(warns[0]) {
			t.Errorf("warnings = %q, want exactly the one remote sentence (no gpu.Open line)", warns)
		}
	})
	t.Run("loopback is read as before", func(t *testing.T) {
		log := fakeMachine(t, "my-desktop", desktopIfaces, dns)
		c, calls := realBackendGPU(t)
		host, warns := collectDecisionHost(context.Background(), DecisionOptions{FSRoot: localityRoot, GPU: c}, "http://127.0.0.1:8091")
		if host.CPU == "" || host.Kernel == "" || host.Hostname != "rig-epyc" || host.HostnameSource != tape.HostnameObserved || len(host.GPUs) == 0 {
			t.Errorf("host = %+v, want the fixture machine and its cards", host)
		}
		if calls.Load() == 0 || len(warns) != 0 || log.count() != 0 {
			t.Errorf("queries %d, warnings %q, lookups %d; want >0, none, 0", calls.Load(), warns, log.count())
		}
	})
	t.Run("this machine's own name is read as before", func(t *testing.T) {
		fakeMachine(t, "my-desktop", desktopIfaces, dns)
		c, _ := realBackendGPU(t)
		host, warns := collectDecisionHost(context.Background(), DecisionOptions{FSRoot: localityRoot, GPU: c}, "http://my-desktop:8091")
		if host.CPU == "" || len(host.GPUs) == 0 || len(warns) != 0 {
			t.Errorf("host = %+v, warnings %q", host, warns)
		}
	})
}
