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
// tokens, and the previous set, sized to answer in 150 to 300, ended on EOS in
// about two seconds: the zero-config clip was a two-second generation and the
// clock never mattered. Each prompt here asks for several parts — every bug
// explained, a rewrite, a test file, a plan to confirm the cause — which a
// capable model writes for thousands of tokens. On a 13.5 tok/s rig the same
// prompt is cut at about 270 tokens, less what its prefill took of the 20 s,
// and on a 2 tok/s rig the floor holds the cut until 64: the prompt does not
// have to know which box it is on. EOS
// arriving first is still possible and still honest, and the tape says which
// happened; the aim is only that our own prompt is not the reason.
//
// The prompts are long. A prompt under tape.MinPrefillPromptTokens is not a
// prefill measurement and the card says so; the previous set was about forty
// tokens and every zero-config card carried that caveat. Each prompt here is
// about 200 to 500 tokens of its own before the chat template adds any, whose
// share differs per model and so is not counted on. The top of that range is
// also its limit: at the 60 tok/s low end of honest prefill on this repo's
// hero rig, 500 tokens is about 8 s before the first token of a 20 s run.
// prompts_test.go pins both ends, in characters, with the conversion it
// assumes. The length comes from the material — a function with a real bug, a
// query plan, a log excerpt — never from filler: a model given filler writes
// filler, and a reader who opens the tape sees it. It is also the workload
// this project is aimed at, since coding agents send exactly this.
//
// They are unlike each other from the first word, so concurrent streams do
// not share a prefix and cannot hit each other's prompt cache, which would
// make the measured prefill meaningless. Material comes first and the
// instruction last, and nothing needs a tool, a file or the network, so any
// instruction-tuned model can answer. Code is fenced with ~~~ so it can sit in
// a Go raw string.
var defaultPrompts = []string{
	`Review this Go rate limiter. It is called from every request goroutine of an HTTP server, keyed by client IP, and in production it panics with "concurrent map writes" a few times a day while the process's memory grows until it is killed.

~~~go
type Limiter struct {
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   float64
}

type bucket struct {
	tokens float64
	last   time.Time
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

Find every bug and risk in it, including the ones that do not cause the panic, and explain each one. Then write a corrected version that is safe for concurrent use and evicts idle keys, and a table-driven test file that covers refill, burst and many concurrent callers under the race detector.`,

	`Diagnose why this PostgreSQL 16 query went from 40 ms to 9 seconds the morning after a nightly import added forty thousand customers and two million orders. The plan is from EXPLAIN (ANALYZE, BUFFERS) on a slow run.

~~~
SELECT c.id, c.name, sum(o.total) AS spent
FROM customers c JOIN orders o ON o.customer_id = c.id
WHERE o.created_at >= now() - interval '30 days' AND c.region = 'EU'
GROUP BY c.id, c.name ORDER BY spent DESC LIMIT 20;

Limit (actual time=9120.4..9120.5 rows=20 loops=1)
  -> Sort (actual time=9120.4..9120.4 rows=20 loops=1)
       -> HashAggregate (rows=3) (actual time=9101.7..9116.2 rows=48211 loops=1)
            -> Nested Loop (rows=12) (actual time=0.9..8870.3 rows=1203311 loops=1)
                 -> Seq Scan on customers c (rows=3) (actual time=0.4..61.2 rows=48211 loops=1)
                      Filter: (region = 'EU')
                 -> Index Scan using orders_customer_id_idx on orders o (rows=4) (actual rows=25 loops=48211)
                      Filter: (created_at >= (now() - '30 days'::interval))
                      Rows Removed by Filter: 212
                      Buffers: shared hit=402113 read=1893022
Execution Time: 9121.0 ms
~~~

Walk through the plan node by node and say what each gap between estimated and actual rows tells you. Rank the likely root causes, give the commands you would run to confirm each one, and propose the index or query change you would make, with the plan you expect afterwards and the write cost it adds.`,

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

Include a summary, the customer impact with numbers, the root cause, and the contributing factors, including why a 5% canary with a healthy error rate did not catch a problem that only appears once retries pile up. Say what went well and what went poorly in the response itself, then list at least six concrete action items, each with an owner role and a way to verify it is done.`,

	`Refactor this Python script, which summarises a 40 GB nginx access log into request counts and 95th percentile latency per route. It is run by hand on a small VM and is killed by the kernel before it prints anything.

~~~python
import re, sys

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

main(sys.argv[1])
~~~

Turn it into a streaming tool that uses bounded memory, keeps the output format but sorts it, and adds command line options to filter by route prefix and status class. Explain each change, including why this percentile is wrong for small samples. Finish with pytest tests, among them percentile cases for one value, twenty values and all values equal.`,

	`Users report that the search box in our React app sometimes shows results for an earlier query than the one they typed, and the network tab shows a request fired for almost every keystroke.

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

Explain every bug in this component, and give the exact sequence of keystrokes and response timings that shows the stale results. Then rewrite it with proper debounce cleanup, request cancellation using AbortController, URL encoding, and loading and error states. Finish with tests using React Testing Library and fake timers that reproduce the original race and prove the rewrite fixes it.`,

	`Intermittent 502 errors started after we put nginx in front of a Node.js API. About one request in two thousand fails, and it is always one that reuses an upstream connection that sat idle for a few seconds. These are the relevant excerpts.

~~~
# nginx error.log
2026/09/13 10:41:07 [error] 311#311: *918273 upstream prematurely closed connection while reading response header from upstream, client: 10.0.4.17, request: "POST /v1/orders HTTP/1.1", upstream: "http://10.0.9.3:3000/v1/orders"

# nginx.conf
upstream api {
    server 10.0.9.3:3000;
    keepalive 64;
}
location /v1/ {
    proxy_pass http://api;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
}

# server.js
const server = app.listen(3000); // keepAliveTimeout left at its default
~~~

Explain step by step the race that produces this error, including which side closes the idle connection and why nginx does not retry the POST on another one. Give the settings on both sides that fix it and the rule for choosing their values, and describe how you would reproduce the failure deterministically in a test environment before and after the change.`,

	`Rustc rejects the function below with E0502, cannot borrow *words as mutable because it is also borrowed as immutable, pointing at the words.push call.

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

Explain, in terms of borrows and lifetimes, exactly why the compiler is right to reject it, and describe the memory bug it would allow if it were accepted. Then fix it three different ways: returning an owned String, working with an index instead of a reference, and splitting it into two functions with a different signature. Say which you would choose and why, make every version handle an empty vector without panicking, and write unit tests for all three.`,

	`Design the PostgreSQL schema for a meeting room booking service with these requirements.

- An organisation has several offices, each in its own time zone, and each office has rooms with a capacity and a set of equipment such as a screen or a phone.
- A booking reserves one room from a start time to an end time for an organiser, with a title and a list of invited people who can accept or decline.
- Two bookings for the same room must never overlap, even when two people press the button at the same moment on different servers.
- Bookings can repeat weekly or on chosen weekdays until an end date, and a single occurrence can be moved or cancelled without touching the rest of the series.
- Cancelled bookings are kept for a year for auditing and must not block the room.

Give the complete DDL with every constraint, and explain how the schema itself, not application code, makes a double booking impossible under concurrent transactions. Show the SQL for finding free rooms for a time slot, listing a person's week, and moving one occurrence of a series, with the indexes each needs, and discuss the trade-offs of storing recurring bookings expanded versus as rules.`,

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

# delete old backups
cd $DEST
ls -t | tail -n +$KEEP | xargs rm -rf

echo "backup of $SRC done"
~~~

List every way this script can fail or do damage, with the exact condition that triggers each one, including how many backups it really keeps. Then rewrite it defensively: strict mode, quoting, a check that the mount is really there, a lock so two runs cannot overlap, a meaningful exit status for cron, and retention that cannot delete anything outside the backup directory. Finish by describing how you would test the rewrite safely without touching real backups.`,

	`Our payments service on Kubernetes restarts roughly every forty minutes under normal load. It is a Spring Boot application on Java 21, and nothing in its own logs mentions an error before the restart.

~~~
$ kubectl describe pod payments-7d9f6c7b8-x2k4q
    Last State:     Terminated
      Reason:       OOMKilled
      Exit Code:    137
    Limits:
      memory:  1Gi
    Requests:
      memory:  1Gi

$ kubectl logs payments-7d9f6c7b8-x2k4q --previous | tail -5
03:14:07 INFO  Started PaymentsApplication in 11.2 seconds
03:14:07 INFO  JVM flags: -XX:MaxRAMPercentage=90 -XX:+UseG1GC
03:41:55 WARN  HikariPool-1 - Thread starvation or clock leap detected (housekeeper delta=48s)
03:52:30 INFO  Exported 18204 settlement rows to object storage (buffered=true)
03:53:02 WARN  GC pause (G1 Evacuation Pause) 2.9s, heap 880M->871M
~~~

Explain what is killing the container and why the JVM never throws OutOfMemoryError first. Show how the heap percentage, metaspace, thread stacks, code cache and direct buffers add up against the 1 GiB limit, and what the Hikari warning and the long GC pause each tell you. Then give a step by step plan to confirm the cause on a live pod, and the configuration and code changes you would make, with the trade-off of each.`,

	`Implement the cache interface below in TypeScript without any library.

~~~ts
interface Cache<K, V> {
  get(key: K): V | undefined; // marks the entry as recently used
  set(key: K, value: V, ttlMs?: number): void;
  delete(key: K): boolean;
  readonly size: number;
}

// Requirements:
// - capacity is fixed at construction; a set past capacity evicts the least recently used entry
// - an expired entry is never returned and does not count toward size
// - get, set and delete are O(1)
// - an optional onEvict(key, value, reason) callback, reason "capacity" | "expired" | "deleted"
// - the clock is injectable, so tests never sleep
~~~

Explain the data structure you choose, and whether a plain Map alone is enough, given that it keeps insertion order. Write the implementation with comments on the subtle parts, especially how expired entries are kept out of size without a timer. Then write a thorough Jest test suite with an injected fake clock, and finish with a short section on what would have to change if many async callers shared the cache and computed missing values on a miss.`,

	`Yesterday a teammate ran the commands below on a shared feature branch, and now three days of commits by two people are gone from it.

~~~
$ git checkout feature/billing
$ git pull
hint: You have divergent branches and need to specify how to reconcile them.
fatal: Need to specify how to reconcile divergent branches.
$ git reset --hard origin/main
HEAD is now at 4e1c9a2 Merge pull request #412 from ci/bump-node
$ git push --force
 + 9b07d31...4e1c9a2 feature/billing -> feature/billing (forced update)
~~~

Explain to them, as a patient senior engineer, exactly what each command did to their local branch and to the remote one, and why the pull refused to run. Say where the lost commits still exist and for how long: their own reflog, the other author's clone, and the hosting service. Give the precise commands to recover the branch from each of those places. Then explain what pull.rebase, pull.ff and push --force-with-lease do, which settings you would recommend for the team and why, and how branch protection would have stopped this.`,

	`Audit this ring buffer from a microcontroller UART driver. ring_put runs in the receive interrupt handler and ring_get runs in the main loop, on a single-core Cortex-M4. Every few hours a byte stream arrives corrupted and, once, the board hard-faulted.

~~~c
#define CAP 64

struct ring {
    char buf[CAP];
    int head; /* next write */
    int tail; /* next read */
};

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

Explain every defect, including the out-of-bounds write and what it overwrites, and whether sharing head and tail between the interrupt and the main loop is safe without disabling interrupts, and what volatile does and does not fix. Give a corrected implementation with a power-of-two capacity, and a host-side C test harness that fills, drains and wraps the buffer many times.`,

	`This Django view renders the order history page. It takes four seconds for customers with many orders, and the database log shows about 1,300 queries for a single page load.

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

Explain where each query comes from and how the count grows with the number of orders and items. Point out the bug in the shipment line when Shipment has a OneToOneField to Order. Rewrite the view with select_related, prefetch_related and pagination, and show a test that fails if the query count ever regresses. Finally, discuss whether the total should be computed in the database for money, and what an order should store instead.`,

	`Port this Python function to idiomatic Go, returning a time.Duration and an error, without calling time.ParseDuration.

~~~python
def parse_duration(s: str) -> float:
    """Parse strings like "1h30m", "45s", "2.5m" or "500ms" into seconds."""
    units = {"h": 3600, "m": 60, "s": 1, "ms": 0.001}
    total, num = 0.0, ""
    i = 0
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

Before writing the port, list the inputs on which the Python version behaves questionably, such as "1.2.3s", non-ASCII digits that isdigit accepts, an empty string and very large hour counts. Say for each one what the Go version should do and why the two languages force different decisions. Write table-driven tests for every case you list, a fuzz test that checks the parser never panics, and a comparison of its behaviour with time.ParseDuration.`,

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

Use table-driven cases for empty input, a trailing newline, no newline at all, a line of exactly max bytes, a line longer than max, a max of zero or less, and multi-byte UTF-8. Add property tests that joining the chunks gives back the input and that no chunk exceeds max unless it is a single line. For each case, state what the current code does, point out any case where it contradicts its doc comment, and propose the fix.`,
}

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
		})
	}
	return out
}
