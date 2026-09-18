package server

import (
	"fmt"

	"github.com/midagedev/toktape/internal/tape"
)

// defaultPrompts is the zero-config prompt set. The north star asks for a
// first run with no flag to learn, so a user who types nothing still gets a
// real measurement, and a concurrent run needs one distinct prompt per stream.
// They are also the text a reader sees on opening a tape, so each is written
// as the request a careful engineer would send a coding assistant: real
// material to work on, then what to do with it (TTP-84, 2026-09-14).
//
// Their shape follows from what ends a run, and that changed with TTP-76: a
// run is aimed in seconds, not tokens. With nothing named it has a 20 s
// wall-clock budget, a floor of tape.MinCutTokens under the cut, and a
// runaway cap of 2048 tokens behind both (DefaultFor, DefaultMaxTokens in
// internal/recorder/limit.go). Keeping a slow rig's run short is the clock's
// job now, not the prompt's. So:
//
// The answers are long. 20 s at 140 tok/s (a 7B on a 3090) is about 2800
// tokens, and the set this one replaced, sized to answer in 150 to 300, ended
// on EOS in about two seconds: the zero-config clip was a two-second
// generation and the clock never mattered. Each prompt here asks for several
// parts — every bug explained, a rewrite, a test file, a plan to confirm the
// cause — which a capable model writes for thousands of tokens. On a 13.5
// tok/s rig the same prompt is cut at about 270 tokens, less what its prefill
// took of the 20 s, and on a 2 tok/s rig the floor holds the cut until 64: the
// prompt does not have to know which box it is on. EOS arriving first is still
// possible and still honest, and the tape says which happened; the aim is only
// that our own prompt is not the reason.
//
// The prompts are long enough to be a prefill measurement and no longer. A
// prompt under tape.MinPrefillPromptTokens is not one and the card says so.
// The ceiling is the newer half of the rule and it was measured, not reasoned
// (lead, 2026-09-14): on this repo's hero rig, two concurrent 357-token
// prompts spent 21 s before the first token — warm, at 16.8 tok/s of prefill
// per stream — which is the whole of a 20 s run. That rig's prefill is bound
// by paging a 445 GB model through 251 GB of RAM, and a box like it is
// precisely the box this tool exists for, so the set is sized for it rather
// than for the rig where prefill is free. prompts_test.go pins both ends in
// characters, with the conversion it assumes.
//
// The length comes from the material — a function with a real bug, a query
// plan, a log excerpt — never from filler: a model given filler writes filler,
// and a reader who opens the tape sees it. It is also the workload this
// project is aimed at, since coding agents send exactly this.
//
// They are unlike each other from the first word, so concurrent streams do
// not share a prefix and cannot hit each other's prompt cache, which would
// make the measured prefill meaningless. Material comes first and the
// instruction last, and nothing needs a tool, a file or the network, so any
// instruction-tuned model can answer. Code is fenced with ~~~ so it can sit in
// a Go raw string.
var defaultPrompts = []string{
	`Review this Go rate limiter, keyed by client IP and called from every request goroutine of an HTTP server. In production it panics with "concurrent map writes" and the process grows until it is killed.

~~~go
type Limiter struct {
	buckets map[string]*bucket // bucket{tokens float64; last time.Time}
	rate    float64            // tokens per second
	burst   float64
}

func (l *Limiter) Allow(key string) bool {
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: time.Now()}
		l.buckets[key] = b
	}
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
~~~

Find every bug and risk, the ones that do not cause the panic included, and explain each. Then write a concurrency-safe version that evicts idle keys, with a table-driven test covering refill, burst and many callers under the race detector.`,

	`Diagnose why this PostgreSQL 16 query went from 40 ms to 9 s after a nightly import added forty thousand customers and two million orders.

~~~
SELECT c.id, c.name, sum(o.total) AS spent
FROM customers c JOIN orders o ON o.customer_id = c.id
WHERE o.created_at >= now() - interval '30 days' AND c.region = 'EU'
GROUP BY c.id, c.name ORDER BY spent DESC LIMIT 20;

-> HashAggregate (rows=3) (actual rows=48211 loops=1)
     -> Nested Loop (rows=12) (actual rows=1203311 loops=1)
          -> Seq Scan on customers c (rows=3) (actual rows=48211 loops=1)
               Filter: (region = 'EU')
          -> Index Scan using orders_customer_id_idx (rows=4) (actual rows=25 loops=48211)
               Rows Removed by Filter: 212
               Buffers: shared hit=402113 read=1893022
Execution Time: 9121.0 ms
~~~

Walk through the plan and say what each gap between estimated and actual rows tells you. Rank the likely causes, give the commands that confirm each, and propose the index or query change you would make, with the plan you expect after it.`,

	`Using the incident timeline below, write a blameless postmortem for the engineering team.

~~~
14:02 deploy of api v2.31 starts, canary at 5% of traffic
14:09 canary error rate 0.4%, inside the 1% threshold; rollout continues
14:21 rollout reaches 100%
14:26 p99 latency on /checkout rises from 180 ms to 2.4 s
14:31 on-call paged by the latency alert; first suspects the database
14:44 database CPU normal; connection pools on api pods saturated at 50/50
14:52 v2.31 found to open a new pooled connection on every payment retry
15:03 rollback to v2.30 started
15:11 latency back to normal; 3,912 checkouts failed during the window
~~~

Include a summary, the customer impact with numbers, the root cause, and the contributing factors — among them why a 5% canary with a healthy error rate missed a problem that appears only once retries pile up. Say what went well and badly in the response, then list at least six action items, each with an owner role and a way to verify it.`,

	`Refactor this Python script, which summarises a 40 GB nginx access log into request counts and 95th percentile latency per route. Run by hand on a small VM, it is killed by the kernel before it prints anything.

~~~python
def p95(values):
    values.sort()
    return values[int(len(values) * 0.95)]

def main(path):
    lines = open(path).readlines()
    by_route = {}
    for line in lines:
        m = re.match(r'(\S+) \S+ \S+ \[(.*?)\] "(\w+) (\S+) \S+" (\d+) (\d+) (\d+)ms', line)
        if not m:
            continue
        route = m.group(4).split("?")[0]
        by_route.setdefault(route, []).append(int(m.group(7)))
    for route, times in by_route.items():
        print(route, len(times), p95(times))
~~~

Turn it into a streaming tool that uses bounded memory, keeps the output format but sorts it, and takes options to filter by route prefix and status class. Explain each change, including why this percentile is wrong for small samples. Finish with pytest cases for one value, twenty values and all values equal.`,

	`The search box in our React app sometimes shows results for an earlier query than the one the user typed, and the network tab shows a request for almost every keystroke.

~~~tsx
export function SearchBox() {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<Result[]>([]);

  useEffect(() => {
    const id = setTimeout(async () => {
      const res = await fetch("/api/search?q=" + query);
      setResults(await res.json());
    }, 300);
  }, [query]);

  return (
    <>
      <input value={query} onChange={e => setQuery(e.target.value)} />
      <ul>{results.map(r => <li>{r.title}</li>)}</ul>
    </>
  );
}
~~~

Explain every bug in it, and give the sequence of keystrokes and response timings that shows the stale results. Then rewrite it with debounce cleanup, cancellation through AbortController, URL encoding, and loading and error states. Finish with React Testing Library tests on fake timers that reproduce the race.`,

	`Intermittent 502s started after we put nginx in front of a Node.js API. About one request in two thousand fails, and it is always one that reuses an upstream connection that sat idle for a few seconds.

~~~
# nginx error.log
[error] 311#311: *918273 upstream prematurely closed connection while reading
response header from upstream, request: "POST /v1/orders HTTP/1.1"

# nginx.conf
upstream api { server 10.0.9.3:3000; keepalive 64; }
location /v1/ {
    proxy_pass http://api;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
}

# server.js
const server = app.listen(3000); // keepAliveTimeout left at its default
~~~

Explain step by step the race that produces this error, including which side closes the idle connection and why nginx does not retry the POST on another one. Give the settings on both sides that fix it and the rule for choosing their values, then describe how you would reproduce the failure deterministically before and after the change.`,

	`Rustc rejects the function below with E0502, cannot borrow *words as mutable because it is also borrowed as immutable, pointing at the push call.

~~~rust
/// Appends an upper-cased copy of the longest word and returns the longest word.
fn longest_word(words: &mut Vec<String>) -> &str {
    let mut best = &words[0];
    for w in words.iter() {
        if w.len() > best.len() {
            best = w;
        }
    }
    words.push(best.to_uppercase());
    best
}
~~~

Explain, in terms of borrows and lifetimes, exactly why the compiler is right to reject it, and describe the memory bug it would allow if it were accepted. Then fix it three ways: returning an owned String, working with an index instead of a reference, and splitting it into two functions with a different signature. Say which you would choose and why, make every version handle an empty vector without panicking, and write unit tests for all three.`,

	`Design the PostgreSQL schema for a meeting room booking service.

- An organisation has offices, each in its own time zone, and each office has rooms with a capacity and equipment such as a screen or a phone.
- A booking reserves one room from a start to an end time for an organiser, with a title and invited people who can accept or decline.
- Two bookings for the same room must never overlap, even when two people press the button at the same moment on different servers.
- Bookings repeat weekly or on chosen weekdays until an end date, and one occurrence can be moved or cancelled without touching the rest.
- Cancelled bookings are kept a year for auditing and must not block the room.

Give the DDL with every constraint, and explain how the schema itself, not application code, makes a double booking impossible under concurrent transactions. Show the SQL for finding free rooms in a slot, listing a person's week, and moving one occurrence, with the indexes each needs.`,

	`Harden this nightly backup script, which runs from cron as root. Last week the NFS mount was missing when it ran, and it deleted files from a directory nobody expected it to touch.

~~~bash
#!/bin/bash
SRC=$1
DEST=/mnt/backup/$(hostname)
KEEP=7

mkdir $DEST/tmp
tar czf $DEST/tmp/backup-$(date +%F).tar.gz $SRC
mv $DEST/tmp/* $DEST/
rm -rf $DEST/tmp

cd $DEST
ls -t | tail -n +$KEEP | xargs rm -rf
echo "backup of $SRC done"
~~~

List every way this script can fail or do damage, with the condition that triggers each one, including how many backups it really keeps. Then rewrite it defensively: strict mode, quoting, a check that the mount is really there, a lock so two runs cannot overlap, an exit status cron can act on, and retention that cannot delete anything outside the backup directory.`,

	`Our payments service on Kubernetes restarts roughly every forty minutes under normal load. It is a Spring Boot application on Java 21, and nothing in its own logs mentions an error before the restart.

~~~
Last State: Terminated  Reason: OOMKilled  Exit Code: 137  Limits: memory 1Gi

03:14:07 INFO  Started PaymentsApplication in 11.2 seconds
03:14:07 INFO  JVM flags: -XX:MaxRAMPercentage=90 -XX:+UseG1GC
03:41:55 WARN  HikariPool-1 - Thread starvation or clock leap detected (housekeeper delta=48s)
03:52:30 INFO  Exported 18204 settlement rows to object storage (buffered=true)
03:53:02 WARN  GC pause (G1 Evacuation Pause) 2.9s, heap 880M->871M
~~~

Explain what is killing the container and why the JVM never throws OutOfMemoryError first. Show how the heap percentage, metaspace, thread stacks, code cache and direct buffers add up against the 1 GiB limit, and what the Hikari warning and the GC pause each tell you. Then give a plan to confirm it on a live pod and the changes you would make.`,

	`Implement the cache interface below in TypeScript without any library.

~~~ts
interface Cache<K, V> {
  get(key: K): V | undefined; // marks the entry as recently used
  set(key: K, value: V, ttlMs?: number): void;
  delete(key: K): boolean;
  readonly size: number;
}

// - capacity is fixed at construction; a set past it evicts the least recently used
// - an expired entry is never returned and does not count toward size
// - get, set and delete are O(1)
// - onEvict(key, value, reason), reason "capacity" | "expired" | "deleted"
// - the clock is injectable, so tests never sleep
~~~

Explain the data structure you choose, and whether a plain Map alone is enough given that it keeps insertion order. Write the implementation with comments on the subtle parts, especially how expired entries are kept out of size without a timer. Then write a Jest suite with an injected fake clock, and close with what would change if many async callers shared the cache and computed missing values on a miss.`,

	`Yesterday a teammate ran the commands below on a shared feature branch, and now three days of commits by two people are gone from it.

~~~
$ git pull
hint: You have divergent branches and need to specify how to reconcile them.
fatal: Need to specify how to reconcile divergent branches.
$ git reset --hard origin/main
HEAD is now at 4e1c9a2 Merge pull request #412 from ci/bump-node
$ git push --force
 + 9b07d31...4e1c9a2 feature/billing -> feature/billing (forced update)
~~~

Explain to them, as a patient senior engineer, what each command did to their local branch and to the remote one, and why the pull refused to run. Say where the lost commits still exist and for how long — their own reflog, the other author's clone, the hosting service — with the commands to recover from each. Then explain pull.rebase, pull.ff and push --force-with-lease, and how branch protection would have stopped this.`,

	`Audit this ring buffer from a UART driver. ring_put runs in the receive interrupt handler and ring_get in the main loop, on a single-core Cortex-M4. Every few hours a byte stream arrives corrupted and, once, the board hard-faulted.

~~~c
#define CAP 64

struct ring { char buf[CAP]; int head; int tail; };

int ring_put(struct ring *r, char c) {
    if ((r->head + 1) % CAP == r->tail)
        return -1;
    r->buf[r->head++] = c;
    if (r->head > CAP)
        r->head = 0;
    return 0;
}

int ring_get(struct ring *r, char *c) {
    if (r->head == r->tail)
        return -1;
    *c = r->buf[r->tail];
    r->tail = (r->tail + 1) % CAP;
    return 0;
}
~~~

Explain every defect, the out-of-bounds write and what it overwrites included, whether sharing head and tail between the interrupt and the main loop is safe without disabling interrupts, and what volatile does and does not fix. Give a corrected implementation with a power-of-two capacity and a host-side test harness that fills, drains and wraps it.`,

	`This Django view renders the order history page. It takes four seconds for customers with many orders, and the database log shows 1,300 queries for a page load.

~~~python
def order_history(request):
    orders = Order.objects.filter(customer=request.user.customer).order_by("-created_at")
    rows = []
    for order in orders:
        items = order.items.all()
        rows.append({
            "id": order.id,
            "date": order.created_at,
            "total": sum(i.price * i.quantity for i in items),
            "status": order.shipment.status if order.shipment else "pending",
            "products": [i.product.name for i in items],
        })
    return render(request, "orders/history.html", {"rows": rows})
~~~

Explain where each query comes from and how the count grows with the orders and items. Point out the bug in the shipment line when Shipment has a OneToOneField to Order. Rewrite the view with select_related, prefetch_related and pagination, and show a test that fails if the query count regresses.`,

	`Port this Python function to idiomatic Go, returning a time.Duration and an error, without time.ParseDuration.

~~~python
def parse_duration(s: str) -> float:
    """Parse "1h30m", "45s", "2.5m" or "500ms" into seconds."""
    units = {"h": 3600, "m": 60, "s": 1, "ms": 0.001}
    total, num, i = 0.0, "", 0
    while i < len(s):
        ch = s[i]
        if ch.isdigit() or ch == ".":
            num += ch
            i += 1
            continue
        unit = s[i:i+2] if s[i:i+2] == "ms" else ch
        if unit not in units or not num:
            raise ValueError(f"bad duration: {s!r}")
        total += float(num) * units[unit]
        num = ""
        i += len(unit)
    if num:
        raise ValueError(f"missing unit in {s!r}")
    return total
~~~

First list the inputs on which the Python version behaves questionably — "1.2.3s", non-ASCII digits that isdigit accepts, an empty string — and say what the Go version should do instead. Then write the port, table-driven tests for every case, and a fuzz test that it never panics.`,

	`Write a complete test file for the Go function below, which a log shipper uses to split text into batches.

~~~go
// ChunkLines splits text into chunks of at most max bytes, breaking only
// after a newline. A single line longer than max becomes its own chunk.
func ChunkLines(text string, max int) []string {
	var chunks []string
	var cur strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if cur.Len()+len(line) > max && cur.Len() > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}
~~~

Use table-driven cases for empty input, a trailing newline, no newline at all, a line of exactly max bytes, a line longer than max, a max of zero or less, and multi-byte UTF-8. Add property tests that joining the chunks gives back the input and that no chunk exceeds max unless it is one line. For each case, state what the code does today and where it contradicts its doc comment.`,
}

// PromptSetID names this set, and every request DefaultPrompts returns
// carries it (StreamRequest.Set). Two records carrying one id must have done
// the same work, so the id changes whenever the prompts do — prompts_test.go
// holds a hash of the set and fails when they drift apart, which is the only
// thing that keeps the promise true.
//
// The version tracks what a release shipped, not what main holds: a set that
// has never been in a release has nothing to be compared against yet, and
// bumping it before then would spend a version on nobody's tape.
const PromptSetID = "prompts@v1"

// DefaultPrompts returns n distinct chat requests in a deterministic order, so
// two runs on the same rig send the same work. n <= 0 yields nil.
//
// Beyond the built-in set the prompts repeat with a numbered lead sentence,
// which keeps them distinct from their second word — the one "Request" token
// in front of the number is all two streams can share — and so keeps every
// stream's prefix its own.
//
// No request carries a cap of its own. The run's cap is the recorder's to set
// (Options.MaxTokens, resolved in internal/recorder/limit.go and written onto
// every request by buildRequests and roundRequests), and a cap here would be a
// second number on that axis — one that wins wherever a request's own cap is
// allowed to, as it is in a multi-round run.
func DefaultPrompts(n int) []StreamRequest {
	if n <= 0 {
		return nil
	}
	out := make([]StreamRequest, 0, n)
	for i := 0; i < n; i++ {
		text := defaultPrompts[i%len(defaultPrompts)]
		if i >= len(defaultPrompts) {
			text = fmt.Sprintf("Request %d. %s", i+1, text)
		}
		out = append(out, StreamRequest{
			Messages: []tape.Message{{Role: "user", Content: text}},
			Set:      PromptSetID,
		})
	}
	return out
}
