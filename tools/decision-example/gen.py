#!/usr/bin/env python3
"""Builds internal/tape/testdata/decision-example.tape: a SYNTHETIC decision-model
run (TTP-192) for the screen and card tracks to draw against before a real
endpoint exists. Every figure here is invented and the summary says so (Note).
Deterministic: same suite in, same bytes out.

usage: gen.py <suite.jsonl> <out.tape>
"""
import gzip, hashlib, json, random, statistics, sys

MS = 1_000_000  # ns per ms
suite_path, out_path = sys.argv[1], sys.argv[2]
raw = open(suite_path, 'rb').read()
rows = [json.loads(l, object_pairs_hook=lambda p: p) for l in raw.decode().splitlines() if l.strip()]

def obj(pairs):  # ordered pairs -> dict (insertion order kept by json.dumps)
    return {k: (obj(v) if isinstance(v, list) and v and isinstance(v[0], tuple) else v) for k, v in pairs}

# Plausible answers per case: (question id -> top option key or p(true) or score level).
ANSWER = {
    'route-01': {'department': 'technical', 'urgency': 2, 'outage': 0.91},
    'invoice-02': {'status': 'overdue', 'large': 0.97},
    'tool-03': {'tool': 'web_search'},
    'guard-04': {'injection': 0.99, 'severity': 2},
    'review-05': {'risk': 0, 'needs_human': 0.18},
    'ko-06': {'intent': 'refund', 'angry': 0.88},
    'sentiment-07': {'sentiment': 'mixed', 'bug_report': 0.83},
    'long-08': {'failed': 0.98, 'stage': 'deploy'},
}
INPUT_TOKENS = {'route-01': 214, 'invoice-02': 198, 'tool-03': 176, 'guard-04': 161,
                'review-05': 203, 'ko-06': 172, 'sentiment-07': 158, 'long-08': 4386}
rng = random.Random(192)

def questions_of(qpairs):
    out = []
    for qid, q in qpairs:
        q = dict(q)
        opts = []
        crit = q.get('criteria')
        if q['type'] == 'choice' and crit:
            opts = [{'key': k, 'text': v} for k, v in crit]
        elif q['type'] == 'score' and crit:
            opts = [{'key': str(i), 'text': t} for i, t in enumerate(crit)]
        d = {'id': qid, 'type': q['type']}
        if q.get('instructions'): d['instructions'] = q['instructions']
        if opts: d['options'] = opts
        out.append(d)
    return out

def answer_of(case, q):
    want = ANSWER[case][q['id']]
    if q['type'] == 'noul':
        p = round(min(0.9999, max(0.0001, want + rng.uniform(-0.01, 0.01))), 4)
        return {'question_id': q['id'], 'type': 'noul', 'noul': p}, {'type': 'noul', 'noul': p}
    keys = [o['key'] for o in q['options']]
    top = str(want) if q['type'] == 'score' else want
    rest = [k for k in keys if k != top]
    ptop = round(0.86 + rng.uniform(0, 0.12), 4)
    w = [rng.uniform(0.2, 1) for _ in rest]
    probs = {top: ptop}
    for k, x in zip(rest, w):
        probs[k] = round((1 - ptop) * x / sum(w), 4)
    ordered = [{'key': k, 'p': probs[k]} for k in keys]
    a = {'question_id': q['id'], 'type': q['type'], 'confidence': ptop, 'probabilities': ordered}
    resp = {'type': q['type'], 'confidence': ptop, 'probabilities': {k: probs[k] for k in keys}}
    if q['type'] == 'choice':
        a['choice'] = top; resp['choice'] = top
    else:
        sc = round(sum(int(k) * probs[k] for k in keys), 4)
        a['score'] = sc; resp['score'] = sc
        resp['legend'] = {o['key']: o['text'] for o in q['options']}
    return a, resp

def latency_ms(case, cold):
    if cold: return 640.0
    base = 412.0 if case == 'long-08' else 36.5
    return round(base + abs(rng.gauss(0, 1.6 if case != 'long-08' else 6)), 3)

records, t = [], 1500 * MS  # one and a half seconds of attach before the first send
SHOW_GAP, BURST_GAP, REPEATS = 1200 * MS, 0.4 * MS, 20
idx = 0
def send(row, rep, phase):
    global t, idx
    case = row_id(row)
    body = obj([p for p in row if p[0] != 'id'])
    qs = questions_of(dict(row)['questions'])
    lat = latency_ms(case, idx == 0)
    answers, resp_answers = [], {}
    for q in qs:
        a, r = answer_of(case, q); answers.append(a); resp_answers[q['id']] = r
    it = INPUT_TOKENS[case]
    timings = {'prompt_n': it, 'prompt_ms': round(lat - 2.9, 3), 'head_ms': 1.4, 'cache_n': 0}
    resp = {'model': body['model'], 'answers': resp_answers,
            'usage': {'input_tokens': it, 'output_tokens': 0}, 'timings': timings}
    rec = {'index': idx, 'case_id': case, 'repeat': rep, 'phase': phase, 'lane': 0,
           'sent_at': int(t), 'answered_at': int(t + lat * MS),
           'request': body, 'response': resp, 'questions': qs, 'answers': answers,
           'input_tokens': it, 'model': body['model'], 'server': timings}
    records.append(rec)
    t += lat * MS
    idx += 1

def row_id(row): return dict(row)['id']

for row in rows:  # one paced pass
    send(row, 0, 'showcase'); t += SHOW_GAP
for rep in range(1, REPEATS + 1):  # back to back
    for row in rows:
        send(row, rep, 'burst'); t += BURST_GAP

lat = lambda r: (r['answered_at'] - r['sent_at']) / MS
warm = [lat(r) for r in records[1:]]
q = statistics.quantiles(warm, n=100, method='inclusive')
burst = [r for r in records if r['phase'] == 'burst']
per_case = []
for row in rows:
    c = row_id(row); xs = [lat(r) for r in records[1:] if r['case_id'] == c]
    per_case.append({'case_id': c, 'input_tokens': INPUT_TOKENS[c], 'warm_p50_ms': round(statistics.median(xs), 3), 'answered': len(xs) + (1 if c == records[0]['case_id'] else 0)})
short = [lat(r) for r in records[1:] if r['input_tokens'] < 1000]
long_ = [lat(r) for r in records[1:] if r['input_tokens'] >= 1000]
pp = statistics.median([r['input_tokens'] / (r['server']['prompt_ms'] / 1000) for r in records[1:]])
dec = {'endpoint': '/v1/systemone', 'model': 'clef-flash', 'suite': 'suite.jsonl',
       'suite_sha': hashlib.sha256(raw).hexdigest(), 'cases': len(rows), 'repeats': REPEATS + 1,
       'concurrency': 1, 'requests': len(records), 'errors': 0, 'timing_source': 'server',
       'engine_warm_p50_ms': round(statistics.median([r['server']['prompt_ms'] + r['server']['head_ms'] for r in records[1:]]), 3),
       'cold_ms': lat(records[0]), 'warm_p50_ms': round(statistics.median(warm), 3),
       'warm_p95_ms': round(q[94], 3), 'warm_mean_ms': round(statistics.mean(warm), 3),
       'input_tokens_min': min(INPUT_TOKENS.values()), 'input_tokens_max': max(INPUT_TOKENS.values()),
       'short_warm_p50_ms': round(statistics.median(short), 3), 'long_warm_p50_ms': round(statistics.median(long_), 3),
       'prefill_per_second': round(pp, 1),
       'requests_per_second': round(len(burst) / ((burst[-1]['answered_at'] - burst[0]['sent_at']) / 1e9), 2),
       'per_case': per_case}
summary = {
    'id': '20261002-140000-clef-flash-q4-k-m', 'toktape_version': 'example',
    'started_at': '2026-10-02T14:00:00+09:00', 'finished_at': '2026-10-02T14:00:30+09:00',
    'server': {'kind': 'bloomery', 'url': ':8080', 'build': '0.1.0 (example)', 'n_slots': 1, 'ctx_size': 8192},
    'model': {'file_name': 'Cloudflare_clef-flash-Q4_K_M.gguf', 'format': 'gguf', 'arch': 'qwen35', 'quant': 'q4_K', 'file_bytes': 5_600_000_000},
    'host': {'hostname_source': 'labelled', 'os': 'linux', 'cpu': 'AMD Ryzen Threadripper PRO 5975WX 32-Cores',
             'cpu_cores': 32, 'cpu_threads': 64, 'ram_bytes': 270071025664,
             'gpus': [{'index': 0, 'name': 'NVIDIA RTX A6000', 'vram_bytes': 51527024640, 'pcie': '4.0 x16', 'peak_bw_bps': 768000000000}]},
    'concurrency': 1, 'mode': 'decision', 'decision': dec,
    'note': 'SYNTHETIC example for TTP-192 screen and card work: every figure is invented by tools/decision-example/gen.py.',
}
tape = {'schema': 1, 'summary': summary, 'requests': [], 'samples': [], 'decisions': records}
with gzip.GzipFile(out_path, 'wb', mtime=0) as f:
    f.write(json.dumps(tape, ensure_ascii=False).encode())
print(f"{out_path}: {len(records)} requests, cold {dec['cold_ms']} ms, warm p50 {dec['warm_p50_ms']} ms, "
      f"p95 {dec['warm_p95_ms']}, short {dec['short_warm_p50_ms']}, long {dec['long_warm_p50_ms']}, {dec['requests_per_second']} req/s, run {t/1e9:.1f} s")
