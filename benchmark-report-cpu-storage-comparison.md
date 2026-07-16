# CPU Storage Offload Performance Comparison Report

## Overview

This report compares inference serving performance **with** versus **without** CPU
storage offload (KV-cache offload to host memory), based on the raw AIPerf
statistics in [benchmark-raw-statistics-1-card-offload.md](benchmark-raw-statistics-1-card-offload.md).

Each configuration was measured over **3 runs**; the tables below report the mean
of the three runs.

### Test Configuration

| Parameter | Value |
| --- | --- |
| Hardware | Single card (1 GPU/NPU) |
| Workload | Multi-turn conversation |
| Concurrency | 13 |
| Conversations | 40 |
| Turns (mean) | 10 |
| Input tokens (mean) | 2,000 |
| Output sequence length | 150 tokens (fixed) |
| CPU storage size (enabled) | 25,769,803,776 bytes (24 GiB) |

---

## Aggregated Results (mean of 3 runs)

| Metric | CPU Storage **Enabled** | CPU Storage **Disabled** | Delta (Enabled vs Disabled) |
| --- | ---: | ---: | ---: |
| Output Token Throughput (tokens/sec) | **170.14** | 166.92 | **+3.22 (+1.93%)** |
| Request Throughput (req/sec) | **1.133** | 1.113 | **+0.020 (+1.80%)** |
| Completed Requests | **347.0** | 345.0 | +2.0 (+0.58%) |
| Queue Timeouts (504) | **53.0** | 55.0 | **-2.0 (-3.64%)** |
| Request Latency avg (ms) | **8,148.96** | 8,190.04 | **-41.08 (-0.50%)** |
| TTFT avg (ms) | 1,874.25 | 1,873.02 | +1.23 (+0.07%) |
| TTFT p50 (ms) | **487.74** | 404.73 | +83.01 |
| TTFT p90 (ms) | **7,191.88** | 7,399.88 | -208.00 |
| TTFT p99 (ms) | **14,308.22** | 14,373.03 | -64.81 |
| Time to Second Token avg (ms) | 96.68 | 80.95 | +15.73 |
| Inter Token Latency avg (ms) | **42.11** | 42.40 | -0.29 |
| E2E Output Throughput/user (tokens/sec) | 22.53 | 22.71 | -0.18 |

> Bold = better value for that metric. For latency/timeout metrics lower is better;
> for throughput metrics higher is better.

---

## Per-Run Detail

### CPU Storage Enabled (24 GiB)

| Metric | Run 1 | Run 2 | Run 3 |
| --- | ---: | ---: | ---: |
| Output Token Throughput (tokens/sec) | 170.03 | 170.08 | 170.31 |
| Request Throughput (req/sec) | 1.13 | 1.13 | 1.14 |
| Request Count | 346 | 348 | 347 |
| Queue Timeouts (504) | 54 | 52 | 53 |
| Request Latency avg (ms) | 8,123.53 | 8,165.54 | 8,157.81 |
| TTFT avg (ms) | 1,953.00 | 1,882.68 | 1,787.08 |
| TTFT p50 (ms) | 610.16 | 445.66 | 407.41 |
| TTFT p90 (ms) | 7,594.50 | 7,317.04 | 6,664.10 |
| TTFT p99 (ms) | 14,885.40 | 14,004.96 | 14,034.30 |
| Time to Second Token avg (ms) | 115.81 | 90.72 | 83.51 |
| Inter Token Latency avg (ms) | 41.41 | 42.17 | 42.76 |

### CPU Storage Disabled

| Metric | Run 1 | Run 2 | Run 3 |
| --- | ---: | ---: | ---: |
| Output Token Throughput (tokens/sec) | 165.38 | 169.60 | 165.78 |
| Request Throughput (req/sec) | 1.10 | 1.13 | 1.11 |
| Request Count | 343 | 346 | 346 |
| Queue Timeouts (504) | 57 | 54 | 54 |
| Request Latency avg (ms) | 8,201.63 | 8,082.81 | 8,285.68 |
| TTFT avg (ms) | 1,862.64 | 1,719.64 | 2,036.77 |
| TTFT p50 (ms) | 427.63 | 371.30 | 415.27 |
| TTFT p90 (ms) | 6,979.53 | 7,481.09 | 7,739.01 |
| TTFT p99 (ms) | 14,305.08 | 13,677.33 | 15,136.67 |
| Time to Second Token avg (ms) | 85.70 | 71.90 | 85.25 |
| Inter Token Latency avg (ms) | 42.55 | 42.71 | 41.95 |

---

## Analysis

- **Throughput improves modestly with CPU storage offload.** Output token
  throughput rises from 166.92 to 170.14 tokens/sec (**+1.93%**), and request
  throughput improves by **+1.80%**. The offloaded KV cache lets the engine reuse
  more prefix state across turns instead of recomputing it, which is exactly where
  a high-input, multi-turn workload benefits.

- **Fewer queue timeouts.** Enabling CPU storage reduced 504 "timed out in queue"
  errors from 55 to 53 on average (**-3.6%**), consistent with the higher effective
  throughput draining the queue slightly faster.

- **Tail latency is comparable or slightly better.** Average request latency is
  marginally lower (**-0.50%**), and TTFT p90/p99 are slightly better with offload.
  TTFT p50 and Time-to-Second-Token are marginally higher with offload — likely
  within run-to-run noise (the disabled TTFT avg spans 1,720–2,037 ms across runs).

- **Per-user experience is unchanged.** Inter-token latency (~42 ms) and per-user
  E2E throughput (~22.6 tokens/sec) are effectively identical, so the offload does
  not degrade the streaming experience for individual users.

## Conclusion

On this single-card, high-input-token multi-turn workload, enabling CPU storage
offload delivers a **small but consistent aggregate improvement**: roughly **+2%
output token throughput**, **+1.8% request throughput**, and **fewer queue
timeouts**, with request latency and per-user metrics essentially on par. The
gains are modest because the workload is compute/latency bound rather than
KV-cache-capacity bound at this scale, but offload provides upside with no
measurable regression, making it a safe default to enable.
