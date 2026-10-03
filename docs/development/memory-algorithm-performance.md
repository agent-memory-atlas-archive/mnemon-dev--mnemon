# Memory algorithm performance audit

Date: 2026-10-03. Production baseline: `4d312c4e`. Optimized implementation:
`766c3965`. Work was isolated in `codex/memory-algorithm-performance`.

[中文报告](../zh/development/memory-algorithm-performance.md)

This audit covers the native Memory read/write pipeline: SQLite retrieval,
keyword and vector scoring, multi-signal recall, semantic candidates, bounded
graph traversal, graph writes and retention. Agency authorization, replay and
daemon protocols are outside this change. There is no claim of a universally
optimal implementation: input size, graph density, selectivity and durability
requirements impose different costs.

## What changed

1. Replace three single-column insight indexes with indexes matching active
   timestamp, source/timestamp and importance/timestamp reads. Remove the old
   indexes so writes do not maintain both sets. Query-plan tests verify that
   these ordered reads no longer need a temporary sort. Existing stores upgrade
   on writable open; read-only stores continue to work without migration.
2. Load bounded BFS neighborhoods through endpoint indexes, keeping edge
   insertion order, self-loop deduplication, deleted-node filtering, depth and
   result limits. Unlimited traversal retains bulk loading. Typed neighborhood
   reads use two indexed branches: a factored OR can otherwise make SQLite scan
   every edge of the requested type.
3. Keep per-query keyword and cosine **scores** instead of retaining every token
   set and embedding vector. Stream embedding blobs, compute cosine once per
   vector and reuse it across anchor selection, beam traversal and reranking.
   Reuse already-loaded active insights and cache successful adjacency reads,
   including empty neighborhoods, across anchors. Caches die with the query.
4. Keep only the best K semantic or embedding-only diff candidates with a typed
   heap. Reuse tokenization of the query in semantic token fallback, and merge
   content/tag/entity tokens directly without allocating separate field maps.
5. Reuse SQL's time ordering when it also matches absolute timestamp ordering;
   imported timestamps with different UTC offsets still get the original sort.
6. Skip known-entity extraction scans in explicitly provided entity mode.
   Prepare insight/edge inserts once per transaction and reuse the statements.
   The transaction closes them on commit/rollback; panic cleanup also rolls back
   before clearing the transaction-local pointers.

The scoring formulas, weights, similarity thresholds, RRF constant, anchor
limits, per-anchor visited sets, beam budgets, supersession demotion, exact
duplicate identity checks, causal ordering and embedding failure fallback are
unchanged. Equal-score/equal-time candidates still have no guaranteed relative
order; the old code already combined unordered maps and non-stable sorts. The
optimization does not add approximate nearest-neighbor search or discard
low-ranked candidates before a stage that needs them for score normalization.

## Complexity audit

Let **N** be active insights, **E** graph edges, **L** average text/tag/entity
size, **d** embedding dimensions, **Q** query tokens and **K** requested results.
**M** is the number of matching rows examined by a filtered SQL read. Hash-map
lookups below use expected constant time. Key/payload sizes matter in addition
to the row count.

| Path | Before | After / remaining cost |
|---|---|---|
| ID lookup | B-tree `O(log N)` plus payload decoding | Unchanged control |
| All active insights, sorted by time | `O(N log N + NL)` with temporary sort | `O(N + NL)` ordered index scan |
| Latest/recent by source; unfiltered ranked LIMIT | Scan matching rows and order them; bounded SQLite LIMIT sorting can be `O(M log K)`, latest-one is `O(M)` | `O(log N + K)` for these index-compatible predicates, plus returned payloads |
| Leading-wildcard LIKE / other residual filters | Worst-case scan of candidates | Still worst-case `O(NL)`; an ordering index cannot prove a negative substring match |
| Entity membership in JSON | Inspect matching JSON entries and order candidates | Can stop after K matches in time order; a miss still visits all N rows and their entity entries. No entity inverted index was introduced |
| Keyword ranking | `O(N(L + Q + log K))` | Same asymptotic bound, fewer allocations; recall retains N scalar scores instead of N token sets |
| Exact vector anchor search | `O(Nd + N log K)` | Same exact search bound; computed cosine scores are reused by downstream stages |
| Semantic candidates / embedding-only diff selection | `O(Nd + M log M)` and `O(M)` selection storage | `O(Nd + M log K + K log K)` and `O(K)` selection storage |
| Semantic token fallback | Re-tokenize both texts per pair, then `O(M log M)` sort | Query tokenized once; `O(NL + M log K + K log K)`. Loading all active insights remains linear |
| RRF and time anchors | Combine at most 60 anchors; copy and sort N timestamps | RRF unchanged; `O(N)` order check with no copy for normal UTC data. Mixed-offset fallback remains `O(N log N)` |
| Final reranking / WHY ordering | Candidate scoring plus `O(C log C)` ranking and causal ordering | Ranking and ordering retained. Keyword/cosine signals reused; superseded lookup stays scoped to candidates and fail-closed |
| WHY topological ordering | Indexed outgoing-edge reads, then a heap-based Kahn traversal | Unchanged: `O(C log E + E_out + C log C)` for C returned nodes and their outgoing causal edges; cycles retain the existing fallback |
| Atomic batch of B inserts | B SQL compilations plus B-tree/JSON/durable write costs | One compilation per insert kind per transaction; still `O(B log(N+B))` index work, plus payload and commit I/O |
| Known-entity set / statistics | Whole-store JSON/aggregate scans | Unchanged when required; provided-entity mode avoids the unused scan |
| Retention refresh | Load N rows, aggregate E edges, compute scores, update N rows, sort candidates | Unchanged scan/update work; generally `O(E + N log N)` with current indexes and bounded payload sizes |
| Automatic capacity pruning | Count active rows, select eligible weakest records, then delete/audit a bounded batch | Active count remains `O(N)`; candidate selection can scan/sort eligible rows. Age, immunity, exclusions and atomic audit writes are unchanged |
| Single-vector cosine / serialization | `O(d)` / linear payload size | Unchanged; avoiding repeated calls provides the improvement |
| Batch import with per-item diff/semantic matching | Repeated exact scans as the corpus grows | Still potentially `O(BN + B²)` for fixed text/vector sizes; this PR reduces component costs, not the exact comparison requirement |

For beam search, let **A <= 60** be anchors, **P** node expansions across all
anchors, **U <= P** distinct expanded nodes, **X** processed transitions and
**E_U** the sum of incident edges loaded for those distinct nodes. The original
path issued up to P adjacency queries, reloaded candidate insights and computed
cosine during each transition and final reranking. The new path issues at most
U successful adjacency queries, uses the active insight snapshot already loaded
at recall start and computes each cosine once. The cosine component changes from
`O((N + X + C)d)` to `O(Nd + X + C)`. Heap traversal itself is unchanged. P is
bounded by the existing depth, beam width and per-anchor visit budgets; dense
adjacency lists still cost time to read. Cached adjacency consumes `O(E_U)`
space, in addition to the active insight snapshot and `O(N)` scalar caches.

For a bounded BFS, let **V_b** be expanded nodes, **R_b** distinct encountered
neighbors and **deg(v)** their incident degrees. Full-store loading is replaced
by approximately `O(V_b log E + sum(deg(v) log deg(v)) + R_b log N)` indexed work,
plus payloads. The degree sort preserves the old insertion order. Work depends
on the neighborhood, including missing/deleted endpoints, rather than unrelated
history. A high-degree node can still be expensive. Unlimited BFS retains its
bulk `O(NL + E)` loading and traversal path.

## Measurement method

The committed [benchmarks](../../internal/memory/performance_test.go) use real
temporary SQLite WAL databases and synthetic local vectors. N is **corpus size**,
not Go's adaptive benchmark iteration count. Sizes are 1, 10, 100, 1,000 and
10,000. There are 128 vector dimensions, fixed-width record IDs/text, two tags,
two entities and ten sources. The regular graph is a causal chain with N-1
edges. A separate dense fixture at N=1/10/100 adds N(N-1) semantic edges.

Read limits are 10, semantic candidate limits remain 3/5 and bounded BFS uses
two hops / 20 results. Entity-hit records all contain `SQLite`; `EntityMiss` and
`RankedKeywordMiss` deliberately scan for absent terms. Pure scoring benchmarks
start with inputs already in memory; recall/BFS include their database reads.
Fixtures use historical timestamps, so EngineProvided measures backbone,
entity and semantic work without current-window proximity edges. It replaces
existing edges inside a transaction, keeping the graph size bounded.

Insert measures N new insights into an empty store in one committed transaction;
fixture construction and cleanup are excluded. Retention includes its existing
effective-importance writes. None of these timings include CLI startup,
provider/network latency, embedding generation or a cold filesystem cache.

Host: Apple M4, Darwin/arm64, Go 1.25.4, `-cpu=1`. The same benchmark sources were
compiled against each production revision. Five independent runs per revision
used `-benchtime=100ms`, alternating before/after order between rounds. Tables
show medians. `cpu-ns/op` is process user+system CPU, including GC, measured with
`getrusage`; it excludes descheduling and blocked I/O. Ordinary `ns/op` remains
wall time. The insert benchmark excludes cleanup from both metrics. Other OSes
still run the benchmarks with wall-time/allocation reporting.

The machine was shared, not an isolated performance lab. CPU measurements,
unchanged control paths, allocation counts, query plans and operation counts
are used together; timing ratios are local observations, not latency promises.
`B/op` is cumulative Go allocation per operation, **not peak RSS** or database
file size. Three timing points alone cannot establish a Big-O bound.

### N=1, 10, 100

CPU **µs/op**, before → after. Each cell is a median of five runs.

| Path | N=1 | N=10 | N=100 |
|---|---:|---:|---:|
| ByID | 10.2 → 10.5 | 10.2 → 10.1 | 10.2 → 10.2 |
| AllActive | 11.2 → 11.3 | 32.9 → 30.7 | 259.4 → 210.1 |
| Ranked | 22.7 → 21.1 | 44.5 → 40.5 | 52.9 → 40.1 |
| SourceLatest | 15.9 → 14.2 | 15.8 → 14.5 | 24.0 → 14.3 |
| SourceRecent | 23.2 → 22.6 | 23.6 → 22.9 | 48.6 → 41.6 |
| Entity | 16.7 → 16.2 | 25.4 → 23.1 | 100.9 → 26.1 |
| EntityMiss | 16.1 → 15.9 | 20.8 → 19.9 | 67.5 → 62.5 |
| RankedKeywordMiss | 22.9 → 21.6 | 25.6 → 24.4 | 59.2 → 55.3 |
| Embeddings | 6.0 → 6.4 | 14.3 → 14.2 | 87.7 → 88.1 |
| Keyword | 4.2 → 3.4 | 37.8 → 30.4 | 355.8 → 290.2 |
| Diff | 7.3 → 6.2 | 74.7 → 64.4 | 440.9 → 368.9 |
| RecallKeyword | 70.9 → 67.8 | 877.3 → 266.4 | 4,839.2 → 2,094.4 |
| RecallHybrid | 78.2 → 75.6 | 919.5 → 283.5 | 6,144.7 → 2,253.8 |
| RecallWhy | 43.7 → 42.0 | 1,121.4 → 344.5 | 7,561.7 → 2,357.0 |
| SemanticEmbedding | 11.5 → 13.1 | 53.5 → 53.0 | 70.6 → 63.2 |
| SemanticTokens | 11.5 → 13.1 | 76.9 → 51.0 | 676.6 → 407.8 |
| Neighborhood | 17.0 → 14.8 | 49.4 → 57.7 | 359.3 → 58.8 |
| Atomic insert batch | 74.8 → 75.0 | 266.4 → 171.7 | 2,246.0 → 1,203.7 |
| KnownEntities | 8.0 → 8.4 | 15.4 → 15.6 | 69.3 → 69.3 |
| Retention | 58.2 → 59.0 | 151.9 → 149.6 | 1,134.6 → 1,104.2 |
| EngineProvided | 97.0 → 85.8 | 388.3 → 300.7 | 1,004.4 → 637.8 |

### Larger corpus and allocation results

CPU **ms/op**, before → after; allocation is **KiB/op** at N=10,000. Speedup is before CPU / after CPU, not a throughput SLA.

| Path | N=1,000 CPU | N=10,000 CPU | Speedup at 10,000 | Allocation at 10,000 |
|---|---:|---:|---:|---:|
| ByID | 0.010 → 0.010 | 0.011 → 0.010 | 1.05× | 2.3 → 2.3 |
| AllActive | 2.449 → 1.976 | 36.536 → 21.287 | 1.72× | 14,914.0 → 14,913.3 |
| Ranked | 0.123 → 0.039 | 2.294 → 0.040 | 57.33× | 15.9 → 15.9 |
| SourceLatest | 0.070 → 0.015 | 1.193 → 0.015 | 81.60× | 2.4 → 2.4 |
| SourceRecent | 0.098 → 0.042 | 1.169 → 0.042 | 27.85× | 15.8 → 15.8 |
| Entity | 0.948 → 0.023 | 18.365 → 0.024 | 779.19× | 1.5 → 1.5 |
| EntityMiss | 0.499 → 0.468 | 10.710 → 6.614 | 1.62× | 0.6 → 0.6 |
| RankedKeywordMiss | 0.375 → 0.372 | 9.417 → 9.796 | 0.96× | 1.3 → 1.3 |
| Embeddings | 0.800 → 0.805 | 14.801 → 14.857 | 1.00× | 16,160.0 → 16,160.0 |
| Keyword | 3.524 → 2.814 | 33.609 → 27.180 | 1.24× | 33,438.9 → 19,688.6 |
| Diff | 4.106 → 3.043 | 35.969 → 28.939 | 1.24× | 35,175.5 → 20,903.8 |
| RecallKeyword | 10.548 → 6.443 | 76.940 → 53.769 | 1.43× | 49,681.0 → 35,820.2 |
| RecallHybrid | 15.744 → 9.435 | 101.097 → 73.694 | 1.37× | 77,488.4 → 58,103.1 |
| RecallWhy | 17.782 → 10.200 | 108.947 → 75.466 | 1.44× | 77,854.1 → 58,215.7 |
| SemanticEmbedding | 0.221 → 0.144 | 1.892 → 0.962 | 1.97× | 966.1 → 12.4 |
| SemanticTokens | 6.516 → 3.933 | 75.362 → 41.223 | 1.83× | 42,593.3 → 22,179.5 |
| Neighborhood | 3.362 → 0.058 | 45.634 → 0.058 | 782.60× | 23,425.1 → 9.3 |
| Atomic insert batch | 42.133 → 35.761 | 550.995 → 458.416 | 1.20× | 12,970.7 → 11,017.7 |
| KnownEntities | 0.584 → 0.581 | 11.751 → 11.645 | 1.01× | 1.5 → 1.5 |
| Retention | 11.675 → 11.813 | 184.412 → 185.037 | 1.00× | 20,025.9 → 20,025.9 |
| EngineProvided | 3.369 → 0.814 | 56.044 → 2.029 | 27.62× | 290.9 → 51.4 |

### Dense graph and unchanged controls

| Dense recall | CPU ms/op, before → after | CPU speedup | KiB/op, before → after |
|---|---:|---:|---:|
| N=1 | 0.059 → 0.058 | 1.02× | 11.2 → 9.8 |
| N=10 | 3.515 → 0.506 | 6.94× | 1,447.7 → 236.9 |
| N=100 | 126.394 → 19.429 | 6.51× | 66,880.2 → 10,048.0 |

At N=10,000, unchanged ID lookup and embedding loading are approximately
1.05× and 1.00×, and retention remains approximately 1.00×. These controls
help distinguish the larger algorithmic gains from timing noise. Negative LIKE
lookup remains linear and used about 4% more CPU in this run. At N=10, bounded
BFS uses 57.7 µs versus 49.4 µs for the bulk baseline: extra indexed calls cost
more on tiny graphs. Single-record semantic lookup also has roughly 1.6 µs of
extra fixed overhead. These regressions are included, not removed from the data.

The larger indexes initially increased batch-write CPU cost. Transaction-local
statement reuse brought the final 10,000-row batch from 551.0 ms to 458.4 ms
(about 17% less CPU) with the same commit boundary. Hybrid recall reduced
per-operation allocation from about 75.7 MiB to 56.7 MiB; dense recall at N=100
dropped from about 65.3 MiB to 9.8 MiB. Bounded neighborhood allocation at
N=10,000 is 9,504 bytes rather than 23,987,320 bytes.

The selector's deterministic comparison-count test forces an eviction for every
new item once K=5 is full. Counts at N=1/10/100/1,000/10,000 are
**0 / 37 / 440 / 4,491 / 44,991**, including final sorting of K items. This supports
the code-derived `O(N log K)` bound without a flaky wall-clock CI assertion.

The complete five-run output for every path, including controls and misses, is
saved as [before](benchmarks/2026-10-03-memory-before.txt) and
[after](benchmarks/2026-10-03-memory-after.txt). No exploratory or superseded
implementation measurements are mixed into these files.

## Correctness and verification

- `go build -o mnemon .` — passed.
- `make test` — passed, including vet, deterministic tests and architecture
  checks. The pure Memory-local top-K leaf is included in the dependency graph
  and regular deterministic test list.
- `go test -race ./internal/memory/... ./cmd/memory -count=1` — passed.
- `MNEMON_EMBED_ENDPOINT=http://127.0.0.1:1 bash scripts/e2e_test.sh` — all
  **258 assertions passed**, with embedding providers unavailable by design.
- Cached traversal is checked against the previous independent implementation
  for all four intents, multiple budgets, multiple anchors, cyclic graphs,
  deleted nodes and mismatched vector dimensions. Scores and via labels agree.
- Indexed bounded BFS is compared with bulk traversal, including order, hop,
  edge identity, limits, filters, self-loops and missing/deleted endpoints.
- Tests cover top-K/full-sort parity, comparison bounds, token-field boundaries,
  request refresh after vector/edge/deletion changes, mixed timestamp offsets,
  index upgrades/reopens, endpoint query plans, transaction commit/rollback,
  statement reuse, edge replacement and panic cleanup.

The Agency/Docker/provider integration umbrella and paid live suite were not
run; these changes do not modify those boundaries. The Memory CLI boundary was
exercised directly. The changes do not add goroutines or change transaction
commit policy, supersession authority or exact-duplicate write suppression.

## Reproduce

On the PR revision:

```sh
go test ./internal/memory -run '^$' -bench BenchmarkMemory \
  -benchmem -benchtime=100ms -count=5 -cpu=1
go test ./internal/memory/internal/topk -run TestComparisonBudget -v
go test ./internal/memory/store -run 'Test(ActiveReadPlans|TraversalReadPlans)' -v
```

For a matched baseline, extract `4d312c4e` into a temporary directory and copy
only `internal/memory/performance*test.go` from this PR into its
`internal/memory/` directory. Compile both with
`go test -c -o <separate-output-binary> ./internal/memory`, then run the binaries
with `-test.run='^$' -test.bench=BenchmarkMemory -test.benchmem
-test.benchtime=100ms -test.count=1 -test.cpu=1` in alternating order for five
rounds. Compare medians of the identically named cases. Do not run other test
suites concurrently with the measurements.

## Remaining limits

Exact keyword/vector matching, JSON entity misses and retention still have
whole-corpus work. Persistent token/entity indexes would need their own
migration, update/delete/rollback and tokenization parity contracts. An ANN
index would additionally need a recall-quality budget, which this task did not
permit sacrificing. Larger composite keys trade some insertion work/storage
for ordered reads; transaction-local statement reuse offsets compilation work,
but does not make durable writes constant-time. Large or unbounded graph walks
still depend on actual graph density. These costs are kept explicit rather than
hidden behind a cache that can become stale between commands.
