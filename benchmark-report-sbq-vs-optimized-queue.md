# SBQ (429 + Retry) vs Optimized Queue — Comparison Report

**Concurrency:** 40 · **Source:** [benchmark-raw-statistics-40-concurrency.md](benchmark-raw-statistics-40-concurrency.md)

## Configurations Compared

| Tag         | Configuration                                                                                             |
| ----------- | --------------------------------------------------------------------------------------------------------- |
| **SBQ+429** | kthena **session boost queue** + Prefix Cache + 2×Least Request + **429 + max wait 15s + max retries 10** |
| **OQ**      | kthena **optimized queue** + Prefix Cache + 2×Least Request                                               |

- **SBQ+429**: averaged over 3 runs (1,200 requests/run).
- **OQ**: averaged over 4 runs (1,152–1,184 requests/run).

---

## Headline Results (averaged)

| Metric                           |                     SBQ+429 |       OQ | Δ (SBQ vs OQ) |
| -------------------------------- | --------------------------: | -------: | :-----------: |
| **TTFT avg (ms)**                |                     1,822.6 |  3,132.9 | **−41.8%** ✅  |
| **TTFT p50 (ms)**                |                       819.2 |    734.2 |    +11.6%     |
| **TTFT p90 (ms)**                |                     4,839.8 |  4,692.5 |     +3.1%     |
| **TTFT p99 (ms)**                |                    12,864.1 | 48,274.3 | **−73.4%** ✅  |
| **Request Latency avg (ms)**     |                     8,815.8 |  9,998.3 | **−11.8%** ✅  |
| **Request Latency p50 (ms)**     |                     7,423.3 |  7,509.3 |     −1.1%     |
| **Request Latency p99 (ms)**     |                    21,135.2 | 54,658.8 | **−61.3%** ✅  |
| **Inter Token Latency avg (ms)** |                        46.9 |     46.1 |     +1.8%     |
| **Inter Token Latency p50 (ms)** |                        41.3 |     43.0 |    −4.0% ✅    |
| **Output tok/user avg (tok/s)**  |                        26.8 |     26.1 |    +2.5% ✅    |
| **Output Throughput (tok/s)**    |                       446.6 |    461.2 |     −3.2%     |
| **Request Throughput (req/s)**   |                        2.98 |     3.08 |     −3.2%     |
| **Requests Retried**             | ~4.0% (203–218 retries/run) |        0 |       —       |

---

## Key Findings

1. **Dramatically lower tail latency.** The Session Boost Queue with `429 + retry` cuts
   **p99 TTFT by ~73%** (12.9s vs 48.3s) and **p99 Request Latency by ~61%** (21.1s vs 54.7s).
   The optimized queue lets a fraction of requests balloon past 50–67s, whereas SBQ+429 keeps
   the worst case bounded (max wait 15s + backpressure).

2. **Lower average latency too.** Average TTFT drops ~42% and average request latency ~12%,
   because backpressure (429) prevents already-busy pods from being overloaded.

3. **Minor p50 trade-off.** Median TTFT is slightly higher (+~85ms, +12%) under SBQ+429 — the
   admission control adds a small queuing cost to some fast-path requests, but this is negligible
   next to the tail-latency gains.

4. **Comparable steady-state performance.** Inter-token latency, per-user token throughput, and
   aggregate throughput are all within ~3% between the two configurations. SBQ+429 achieves its
   tail-latency wins **without sacrificing** streaming smoothness or user-perceived speed.

5. **Cost: retries.** SBQ+429 rejects overloaded requests with HTTP 429, so ~4% of requests are
   retried (avg ~4.3 retries per retried request, max 8). This is the mechanism that converts
   unbounded tail latency into bounded, predictable behavior.

---

## Conclusion

The **session boost queue + 429 backpressure + bounded retry** configuration is the clear winner
for **latency-sensitive, high-concurrency** workloads. It trades a small increase in median TTFT
and ~4% request retries for **massive reductions in tail latency (p99 TTFT −73%, p99 latency −61%)**
and lower average latency, while keeping throughput essentially unchanged. The **optimized queue**
delivers marginally higher raw throughput (~3%) but suffers from long, unbounded tails that make
it less suitable when predictable latency (SLO adherence) matters.

> **Recommendation:** Use **SBQ + 429 + max wait 15s + max retries 10** when p99 latency / SLO
> predictability is the priority. Use **Optimized Queue** only when maximizing raw throughput and
> tail latency is not a concern.
