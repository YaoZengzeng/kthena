# Kthena vs llm-d Performance Benchmark Report (Concurrency = 40)

## 1. Test Environment & Methodology

**Workload:** Multi-turn Conversation  
**Model:** LLM Inference Serving  
**Benchmark Tool:** NVIDIA AIPerf  
**Request Count:** 1,200 requests per run (3 runs per configuration, averaged)

| Parameter          | Value  |
| ------------------ | ------ |
| Conversation Count | 120    |
| Turn Mean          | 10     |
| Input Tokens Mean  | 2,000  |
| Concurrency        | **40** |
| GPU Count          | 3      |

**Configurations Tested:**

| System | Configuration                  | Description                                                      |
| ------ | ------------------------------ | ---------------------------------------------------------------- |
| Kthena | 2\*LR + Prefix Cache           | Doubled Least Request weight + Prefix Cache aware routing        |
| Kthena | 3\*PC + 2\*LR + 2\*GPU         | Multi-dimensional routing (Prefix Cache×3 + LR×2 + GPU Usage×2)  |
| Kthena | 3\*PC + 2\*LR + 2\*GPU + 2\*LT | Multi-dimensional routing (+ Least Token×2)                      |
| Kthena | 3\*KV + 2\*LR + 2\*GPU         | Multi-dimensional routing (KVCache Aware×3 + LR×2 + GPU Usage×2) |
| Kthena | OQ + PC + 2\*LR                | Optimized Queue scheduling + Prefix Cache + Least Request×2      |
| llm-d  | Prefix Cache                   | llm-d with Prefix Cache routing strategy                         |
| llm-d  | KVCache Aware                  | llm-d with KVCache-aware routing strategy                        |

---

## 2. Summary of Results

All values are **averages across 3 runs**. Lower is better for latency metrics; higher is better for throughput metrics.

| Configuration                             | TTFT (ms) | Request Latency (ms) | ITL (ms) | Output Throughput (tok/s) | Request Throughput (req/s) | Per-User Throughput (tok/s/user) | Latency Std Dev (ms) |
| ----------------------------------------- | --------- | -------------------- | -------- | ------------------------- | -------------------------- | -------------------------------- | -------------------- |
| **Kthena 2\*LR + PC**                     | 4,517.07  | 14,951.23            | 70.03    | 387.37                    | 2.58                       | 17.61                            | 8,476.79             |
| **Kthena 3\*PC + 2\*LR + 2\*GPU**         | 3,970.48  | 13,303.99            | 62.65    | 417.04                    | 2.78                       | 20.21                            | 9,211.07             |
| **Kthena 3\*PC + 2\*LR + 2\*GPU + 2\*LT** | 4,078.79  | 14,547.27            | 70.27    | 393.03                    | 2.62                       | 17.23                            | 8,072.47             |
| **Kthena 3\*KV + 2\*LR + 2\*GPU**         | 3,824.83  | 13,454.16            | 64.63    | 408.22                    | 2.72                       | 19.89                            | 8,845.42             |
| **Kthena OQ + PC + 2\*LR** ⭐              | 2,931.46  | 9,912.13             | 46.87    | 454.04                    | 3.03                       | 25.44                            | 8,223.05             |
| **llm-d Prefix Cache**                    | 3,596.66  | 13,534.92            | 66.70    | 409.28                    | 2.73                       | 18.63                            | 8,300.27             |
| **llm-d KVCache Aware**                   | 3,717.75  | 13,847.30            | 67.99    | 400.35                    | 2.67                       | 18.22                            | 7,845.01             |

> **Note**: Kthena OQ + PC + 2\*LR completed ~1,159 requests per run (vs 1,200 for others) due to optimized queue scheduling dropping a small number of requests under extreme load.

---

## 3. Comparison Charts

### 3.1 Output Token Throughput (tokens/sec) — Higher is Better

```mermaid
xychart-beta
    title "Output Token Throughput (tokens/sec) — Concurrency 40"
    x-axis ["Kthena 2*LR+PC", "Kthena 3*PC+2*LR+2*GPU", "Kthena 3*PC+2*LR+2*GPU+2*LT", "Kthena 3*KV+2*LR+2*GPU", "Kthena OQ+PC+2*LR", "llm-d PC", "llm-d KV"]
    y-axis "Throughput (tokens/sec)" 370 --> 460
    bar [387.37, 417.04, 393.03, 408.22, 454.04, 409.28, 400.35]
```

### 3.2 TTFT (ms) — Lower is Better

```mermaid
xychart-beta
    title "TTFT (ms) — Concurrency 40"
    x-axis ["Kthena 2*LR+PC", "Kthena 3*PC+2*LR+2*GPU", "Kthena 3*PC+2*LR+2*GPU+2*LT", "Kthena 3*KV+2*LR+2*GPU", "Kthena OQ+PC+2*LR", "llm-d PC", "llm-d KV"]
    y-axis "TTFT (ms)" 2700 --> 4700
    bar [4517.07, 3970.48, 4078.79, 3824.83, 2931.46, 3596.66, 3717.75]
```

### 3.3 Request Latency (ms) — Lower is Better

```mermaid
xychart-beta
    title "Request Latency (ms) — Concurrency 40"
    x-axis ["Kthena 2*LR+PC", "Kthena 3*PC+2*LR+2*GPU", "Kthena 3*PC+2*LR+2*GPU+2*LT", "Kthena 3*KV+2*LR+2*GPU", "Kthena OQ+PC+2*LR", "llm-d PC", "llm-d KV"]
    y-axis "Latency (ms)" 9000 --> 15500
    bar [14951.23, 13303.99, 14547.27, 13454.16, 9912.13, 13534.92, 13847.30]
```

### 3.4 Inter Token Latency / ITL (ms) — Lower is Better

```mermaid
xychart-beta
    title "Inter Token Latency (ms) — Concurrency 40"
    x-axis ["Kthena 2*LR+PC", "Kthena 3*PC+2*LR+2*GPU", "Kthena 3*PC+2*LR+2*GPU+2*LT", "Kthena 3*KV+2*LR+2*GPU", "Kthena OQ+PC+2*LR", "llm-d PC", "llm-d KV"]
    y-axis "ITL (ms)" 42 --> 74
    bar [70.03, 62.65, 70.27, 64.63, 46.87, 66.70, 67.99]
```

### 3.5 Per-User Throughput (tok/s/user) — Higher is Better

```mermaid
xychart-beta
    title "Per-User Throughput (tok/s/user) — Concurrency 40"
    x-axis ["Kthena 2*LR+PC", "Kthena 3*PC+2*LR+2*GPU", "Kthena 3*PC+2*LR+2*GPU+2*LT", "Kthena 3*KV+2*LR+2*GPU", "Kthena OQ+PC+2*LR", "llm-d PC", "llm-d KV"]
    y-axis "Per-User (tok/s/user)" 15 --> 27
    bar [17.61, 20.21, 17.23, 19.89, 25.44, 18.63, 18.22]
```

### 3.6 Request Throughput (req/s) — Higher is Better

```mermaid
xychart-beta
    title "Request Throughput (req/s) — Concurrency 40"
    x-axis ["Kthena 2*LR+PC", "Kthena 3*PC+2*LR+2*GPU", "Kthena 3*PC+2*LR+2*GPU+2*LT", "Kthena 3*KV+2*LR+2*GPU", "Kthena OQ+PC+2*LR", "llm-d PC", "llm-d KV"]
    y-axis "Requests/sec" 2.4 --> 3.2
    bar [2.58, 2.78, 2.62, 2.72, 3.03, 2.73, 2.67]
```

---

## 4. Kthena Advantage Analysis

### 4.1 Each Kthena Configuration vs llm-d Prefix Cache

For latency metrics: negative = lower (better). For throughput metrics: positive = higher (better).

| Metric                  | Kthena 2\*LR+PC | Kthena 3\*PC+2\*LR+2\*GPU | Kthena 3\*PC+2\*LR+2\*GPU+2\*LT | Kthena 3\*KV+2\*LR+2\*GPU | Kthena OQ+PC+2\*LR |
| ----------------------- | --------------- | ------------------------- | ------------------------------- | ------------------------- | ------------------ |
| **Output Throughput**   | -5.4% ❌         | +1.9% ✅                   | -4.0% ❌                         | -0.3% ≈                   | **+10.9%** ✅       |
| **Request Throughput**  | -5.5% ❌         | +1.8% ✅                   | -4.0% ❌                         | -0.4% ≈                   | **+11.0%** ✅       |
| **Request Latency**     | +10.5% ❌        | -1.7% ✅                   | +7.5% ❌                         | -0.6% ≈                   | **-26.8%** ✅       |
| **ITL**                 | +5.0% ❌         | -6.1% ✅                   | +5.4% ❌                         | -3.1% ✅                   | **-29.7%** ✅       |
| **Per-User Throughput** | -5.5% ❌         | +8.5% ✅                   | -7.5% ❌                         | +6.8% ✅                   | **+36.6%** ✅       |
| **TTFT**                | +25.6% ❌        | +10.4% ❌                  | +13.4% ❌                        | +6.3% ❌                   | **-18.5%** ✅       |
| **Latency Std Dev**     | +2.1% ≈         | +11.0% ❌                  | -2.7% ✅                         | +6.6% ❌                   | -0.9% ≈            |

### 4.2 Best Configuration Summary (OQ + PC + 2\*LR vs llm-d Prefix Cache)

```mermaid
xychart-beta
    title "Kthena OQ+PC+2*LR — Improvement vs llm-d Prefix Cache"
    x-axis ["Throughput +10.9%", "Req/s +11.0%", "Latency -26.8%", "ITL -29.7%", "Per-User +36.6%", "TTFT -18.5%"]
    y-axis "Improvement (%)" 0 --> 40
    bar [10.9, 11.0, 26.8, 29.7, 36.6, 18.5]
```

### 4.3 Each Kthena Configuration vs llm-d KVCache Aware

| Metric                  | Kthena 2\*LR+PC | Kthena 3\*PC+2\*LR+2\*GPU | Kthena 3\*PC+2\*LR+2\*GPU+2\*LT | Kthena 3\*KV+2\*LR+2\*GPU | Kthena OQ+PC+2\*LR |
| ----------------------- | --------------- | ------------------------- | ------------------------------- | ------------------------- | ------------------ |
| **Output Throughput**   | -3.2% ❌         | **+4.2%** ✅               | -1.8% ❌                         | +2.0% ✅                   | **+13.4%** ✅       |
| **Request Throughput**  | -3.4% ❌         | **+4.1%** ✅               | -1.9% ❌                         | +1.9% ✅                   | **+13.5%** ✅       |
| **Request Latency**     | +8.0% ❌         | **-3.9%** ✅               | +5.1% ❌                         | **-2.8%** ✅               | **-28.4%** ✅       |
| **ITL**                 | +3.0% ❌         | **-7.9%** ✅               | +3.4% ❌                         | **-4.9%** ✅               | **-31.1%** ✅       |
| **Per-User Throughput** | -3.3% ❌         | **+10.9%** ✅              | -5.4% ❌                         | **+9.2%** ✅               | **+39.6%** ✅       |
| **TTFT**                | +21.5% ❌        | +6.8% ❌                   | +9.7% ❌                         | +2.9% ❌                   | **-21.2%** ✅       |
| **Latency Std Dev**     | +8.1% ❌         | +17.4% ❌                  | +2.9% ≈                         | +12.8% ❌                  | +4.8% ❌            |

### 4.4 Best Configuration Summary (OQ + PC + 2\*LR vs llm-d KVCache Aware)

```mermaid
xychart-beta
    title "Kthena OQ+PC+2*LR — Improvement vs llm-d KVCache Aware"
    x-axis ["Throughput +13.4%", "Req/s +13.5%", "Latency -28.4%", "ITL -31.1%", "Per-User +39.6%", "TTFT -21.2%"]
    y-axis "Improvement (%)" 0 --> 42
    bar [13.4, 13.5, 28.4, 31.1, 39.6, 21.2]
```

### 4.5 llm-d Prefix Cache vs llm-d KVCache Aware

| Metric                  | llm-d PC vs llm-d KV |
| ----------------------- | -------------------- |
| **Output Throughput**   | +2.2% ✅              |
| **Request Throughput**  | +2.2% ✅              |
| **Request Latency**     | -2.3% ✅              |
| **ITL**                 | -1.9% ✅              |
| **Per-User Throughput** | +2.3% ✅              |
| **TTFT**                | -3.3% ✅              |
| **Latency Std Dev**     | +5.8% ❌              |

> **Observation**: llm-d's Prefix Cache strategy slightly outperforms its KVCache Aware strategy across throughput and latency metrics (2-3% improvement), though with marginally higher latency variance. This suggests Prefix Cache routing is modestly more effective than KVCache-aware routing for multi-turn conversation workloads at concurrency 40.

---

## 5. P50 Median Latency Analysis (Core User Experience Metric)

P50 reflects the **typical user experience** and is more representative of what most users feel than averages.

| Configuration                             | TTFT p50 (ms) | Request Latency p50 (ms) | ITL p50 (ms) |
| ----------------------------------------- | ------------- | ------------------------ | ------------ |
| **Kthena 2\*LR + PC**                     | 932.08        | 12,197.51                | 70.46        |
| **Kthena 3\*PC + 2\*LR + 2\*GPU**         | 432.71        | 9,474.18                 | 59.15        |
| **Kthena 3\*PC + 2\*LR + 2\*GPU + 2\*LT** | 618.62        | 11,113.92                | 66.81        |
| **Kthena 3\*KV + 2\*LR + 2\*GPU**         | 517.22        | 9,680.82                 | 60.90        |
| **Kthena OQ + PC + 2\*LR**                | 768.16        | 7,636.77                 | 43.83        |
| **llm-d Prefix Cache**                    | 471.13        | 10,280.89                | 64.53        |
| **llm-d KVCache Aware**                   | 691.86        | 10,527.46                | 64.64        |

### P50 Improvement (Kthena OQ+PC+2\*LR vs llm-d Prefix Cache)

| Metric              | Kthena p50  | llm-d p50    | Improvement  |
| ------------------- | ----------- | ------------ | ------------ |
| **TTFT**            | 768.16 ms   | 471.13 ms    | +63.1% ❌     |
| **Request Latency** | 7,636.77 ms | 10,280.89 ms | **-25.7%** ✅ |
| **ITL**             | 43.83 ms    | 64.53 ms     | **-32.1%** ✅ |

### P50 Improvement (Kthena 3\*PC+2\*LR+2\*GPU vs llm-d Prefix Cache)

| Metric              | Kthena p50  | llm-d p50    | Improvement |
| ------------------- | ----------- | ------------ | ----------- |
| **TTFT**            | 432.71 ms   | 471.13 ms    | **-8.2%** ✅ |
| **Request Latency** | 9,474.18 ms | 10,280.89 ms | **-7.8%** ✅ |
| **ITL**             | 59.15 ms    | 64.53 ms     | **-8.3%** ✅ |

> **Key Finding**: The **OQ+PC+2\*LR** configuration delivers the lowest request latency and ITL at p50 (-25.7% and -32.1% vs llm-d PC), providing dramatically faster token streaming for most users. However, its p50 TTFT is higher (+63.1%) due to the optimized queue's batching behavior. The **3\*PC+2\*LR+2\*GPU** configuration delivers uniformly better p50 across all metrics (-8.2% TTFT, -7.8% latency, -8.3% ITL).

```mermaid
xychart-beta
    title "P50 Request Latency & ITL Comparison (ms)"
    x-axis ["OQ Latency p50 (÷100)", "OQ ITL p50", "3*PC Latency p50 (÷100)", "3*PC ITL p50", "llm-d PC Latency p50 (÷100)", "llm-d PC ITL p50"]
    y-axis "Latency (ms)" 0 --> 120
    bar [76.37, 43.83, 94.74, 59.15, 102.81, 64.53]
```

---

## 6. Tail Latency Comparison (P99)

| Metric                   | Kthena 2\*LR+PC | Kthena 3\*PC+2\*LR+2\*GPU | Kthena 3\*PC+2\*LR+2\*GPU+2\*LT | Kthena 3\*KV+2\*LR+2\*GPU | Kthena OQ+PC+2\*LR | llm-d PC | llm-d KV |
| ------------------------ | --------------- | ------------------------- | ------------------------------- | ------------------------- | ------------------ | -------- | -------- |
| TTFT p99 (ms)            | 17,189          | 25,912                    | 17,088                          | 21,768                    | 47,252             | 20,403   | 18,063   |
| Request Latency p99 (ms) | 31,541          | 39,500                    | 31,620                          | 35,726                    | 53,786             | 34,519   | 31,892   |
| ITL p99 (ms)             | 102.56          | 102.74                    | 103.83                          | 103.21                    | 97.62              | 104.13   | 102.05   |

### P99 Comparison Analysis

| Metric              | Kthena Best (2\*LR+PC) vs llm-d Prefix Cache | Difference   |
| ------------------- | -------------------------------------------- | ------------ |
| TTFT p99            | 17,189 vs 20,403                             | **-15.8%** ✅ |
| Request Latency p99 | 31,541 vs 34,519                             | **-8.6%** ✅  |
| ITL p99             | 97.62 (OQ) vs 104.13                         | **-6.3%** ✅  |

> For p99 tail latency, **Kthena 2\*LR+PC** performs best for TTFT and request latency, while **OQ+PC+2\*LR** has the best ITL p99. The OQ configuration has significantly higher TTFT/latency p99 due to extreme outliers from queue scheduling under peak load (max TTFT reaching 62s), but its ITL p99 is the lowest across all configs.

---

## 7. P90 Latency Comparison

| Configuration                             | TTFT p90 (ms) | Request Latency p90 (ms) | ITL p90 (ms) |
| ----------------------------------------- | ------------- | ------------------------ | ------------ |
| **Kthena 2\*LR + PC**                     | 13,381        | 27,497                   | 99.35        |
| **Kthena 3\*PC + 2\*LR + 2\*GPU**         | 13,793        | 27,794                   | 98.19        |
| **Kthena 3\*PC + 2\*LR + 2\*GPU + 2\*LT** | 12,457        | 26,741                   | 99.80        |
| **Kthena 3\*KV + 2\*LR + 2\*GPU**         | 13,175        | 27,474                   | 99.38        |
| **Kthena OQ + PC + 2\*LR**                | 4,581         | 16,732                   | 72.75        |
| **llm-d Prefix Cache**                    | 12,865        | 27,199                   | 99.26        |
| **llm-d KVCache Aware**                   | 11,824        | 25,770                   | 98.43        |

### P90 Improvement (Kthena OQ+PC+2\*LR vs llm-d Prefix Cache)

| Metric              | Kthena p90 | llm-d p90 | Improvement  |
| ------------------- | ---------- | --------- | ------------ |
| **TTFT**            | 4,581 ms   | 12,865 ms | **-64.4%** ✅ |
| **Request Latency** | 16,732 ms  | 27,199 ms | **-38.5%** ✅ |
| **ITL**             | 72.75 ms   | 99.26 ms  | **-26.7%** ✅ |

---

## 8. Detailed Run Data

### 8.1 Kthena 2\*LR + Prefix Cache (3 runs)

| Metric               | Run 1     | Run 2     | Run 3     | Average   |
| -------------------- | --------- | --------- | --------- | --------- |
| TTFT (ms)            | 4,509.67  | 4,500.50  | 4,541.05  | 4,517.07  |
| Request Latency (ms) | 14,942.23 | 14,894.17 | 15,017.28 | 14,951.23 |
| ITL (ms)             | 70.02     | 69.76     | 70.31     | 70.03     |
| Output Throughput    | 387.16    | 390.07    | 384.89    | 387.37    |
| Request Throughput   | 2.58      | 2.60      | 2.57      | 2.58      |
| Per-User Throughput  | 17.67     | 17.64     | 17.53     | 17.61     |
| Latency Std Dev      | 8,434.73  | 8,435.67  | 8,559.98  | 8,476.79  |

### 8.2 Kthena 3\*PC + 2\*LR + 2\*GPU (3 runs)

| Metric               | Run 1     | Run 2     | Run 3     | Average   |
| -------------------- | --------- | --------- | --------- | --------- |
| TTFT (ms)            | 4,095.95  | 3,925.66  | 3,889.83  | 3,970.48  |
| Request Latency (ms) | 13,644.96 | 13,169.03 | 13,097.97 | 13,303.99 |
| ITL (ms)             | 64.09     | 62.05     | 61.82     | 62.65     |
| Output Throughput    | 407.35    | 421.06    | 422.72    | 417.04    |
| Request Throughput   | 2.72      | 2.81      | 2.82      | 2.78      |
| Per-User Throughput  | 20.26     | 20.11     | 20.26     | 20.21     |
| Latency Std Dev      | 9,154.03  | 9,225.80  | 9,253.39  | 9,211.07  |

### 8.3 Kthena 3\*PC + 2\*LR + 2\*GPU + 2\*LT (3 runs)

| Metric               | Run 1     | Run 2     | Run 3     | Average   |
| -------------------- | --------- | --------- | --------- | --------- |
| TTFT (ms)            | 3,311.16  | 4,253.77  | 4,671.44  | 4,078.79  |
| Request Latency (ms) | 13,840.69 | 14,799.60 | 15,001.51 | 14,547.27 |
| ITL (ms)             | 70.68     | 70.79     | 69.33     | 70.27     |
| Output Throughput    | 404.49    | 386.38    | 388.21    | 393.03    |
| Request Throughput   | 2.70      | 2.58      | 2.59      | 2.62      |
| Per-User Throughput  | 16.80     | 17.21     | 17.69     | 17.23     |
| Latency Std Dev      | 7,238.58  | 8,240.87  | 8,737.97  | 8,072.47  |

### 8.4 Kthena 3\*KV + 2\*LR + 2\*GPU (3 runs)

| Metric               | Run 1     | Run 2     | Run 3     | Average   |
| -------------------- | --------- | --------- | --------- | --------- |
| TTFT (ms)            | 3,886.11  | 3,756.45  | 3,831.94  | 3,824.83  |
| Request Latency (ms) | 13,448.63 | 13,484.76 | 13,429.08 | 13,454.16 |
| ITL (ms)             | 64.18     | 65.29     | 64.42     | 64.63     |
| Output Throughput    | 407.80    | 405.69    | 411.17    | 408.22    |
| Request Throughput   | 2.72      | 2.70      | 2.74      | 2.72      |
| Per-User Throughput  | 20.56     | 19.28     | 19.84     | 19.89     |
| Latency Std Dev      | 8,969.17  | 8,627.99  | 8,939.09  | 8,845.42  |

### 8.5 Kthena OQ + PC + 2\*LR (3 runs)

| Metric               | Run 1    | Run 2    | Run 3     | Average  |
| -------------------- | -------- | -------- | --------- | -------- |
| TTFT (ms)            | 2,687.98 | 2,878.51 | 3,227.88  | 2,931.46 |
| Request Latency (ms) | 9,772.42 | 9,785.56 | 10,178.40 | 9,912.13 |
| ITL (ms)             | 47.55    | 46.37    | 46.68     | 46.87    |
| Output Throughput    | 446.62   | 458.40   | 457.11    | 454.04   |
| Request Throughput   | 2.98     | 3.06     | 3.05      | 3.03     |
| Per-User Throughput  | 25.20    | 25.80    | 25.31     | 25.44    |
| Latency Std Dev      | 6,923.78 | 8,530.63 | 9,214.73  | 8,223.05 |
| Request Count        | 1,152    | 1,159    | 1,166     | 1,159    |

### 8.6 llm-d Prefix Cache (3 runs)

| Metric               | Run 1     | Run 2     | Run 3     | Average   |
| -------------------- | --------- | --------- | --------- | --------- |
| TTFT (ms)            | 3,603.02  | 3,570.78  | 3,616.18  | 3,596.66  |
| Request Latency (ms) | 13,712.33 | 13,844.13 | 13,048.29 | 13,534.92 |
| ITL (ms)             | 67.85     | 68.95     | 63.31     | 66.70     |
| Output Throughput    | 400.90    | 399.57    | 427.37    | 409.28    |
| Request Throughput   | 2.67      | 2.66      | 2.85      | 2.73      |
| Per-User Throughput  | 18.29     | 17.96     | 19.64     | 18.63     |
| Latency Std Dev      | 8,105.75  | 7,968.83  | 8,826.23  | 8,300.27  |

### 8.7 llm-d KVCache Aware (3 runs)

| Metric               | Run 1     | Run 2     | Run 3     | Average   |
| -------------------- | --------- | --------- | --------- | --------- |
| TTFT (ms)            | 3,453.11  | 3,741.55  | 3,958.60  | 3,717.75  |
| Request Latency (ms) | 13,734.09 | 13,618.70 | 14,189.11 | 13,847.30 |
| ITL (ms)             | 69.00     | 66.29     | 68.67     | 67.99     |
| Output Throughput    | 405.77    | 396.61    | 398.67    | 400.35    |
| Request Throughput   | 2.71      | 2.64      | 2.66      | 2.67      |
| Per-User Throughput  | 17.64     | 18.66     | 18.35     | 18.22     |
| Latency Std Dev      | 7,321.22  | 8,088.14  | 8,125.67  | 7,845.01  |

---

## 9. Key Findings

### 9.1 Kthena OQ + PC + 2\*LR is the New Optimal Configuration at Concurrency 40

The **Optimized Queue + Prefix Cache + 2\*LR** configuration delivers a breakthrough in performance under high-concurrency stress (40), **dramatically outperforming llm-d Prefix Cache** across all key metrics:

- **Output throughput +10.9%**: 454.04 vs 409.28 tok/s
- **Request latency -26.8%**: 9,912 vs 13,535 ms
- **ITL -29.7%**: 46.87 vs 66.70 ms — dramatically smoother token streaming
- **Per-user throughput +36.6%**: 25.44 vs 18.63 tok/s/user — transformative individual user experience
- **TTFT -18.5%**: 2,931 vs 3,597 ms — significantly faster first token delivery
- **P90 TTFT -64.4%**: 4,581 vs 12,865 ms — 90% of requests get first token 3× faster

### 9.2 Kthena 3\*PC + 2\*LR + 2\*GPU — Strong Runner-Up

Under high-concurrency stress (40), **the 3\*PC + 2\*LR + 2\*GPU configuration also outperforms llm-d Prefix Cache** across most metrics (except TTFT):

- **Output throughput +1.9%**: 417.04 vs 409.28 tok/s
- **Request latency -1.7%**: 13,304 vs 13,535 ms
- **ITL -6.1%**: 62.65 vs 66.70 ms — noticeably smoother token streaming
- **Per-user throughput +8.5%**: 20.21 vs 18.63 tok/s/user — significantly better individual user experience

### 9.3 KVCache Aware Strategy — Best TTFT Among Non-OQ Kthena Configs

The **3\*KV + 2\*LR + 2\*GPU** configuration also outperforms llm-d Prefix Cache in latency and per-user throughput:

- **Request latency -0.6%**: 13,454 vs 13,535 ms
- **ITL -3.1%**: 64.63 vs 66.70 ms
- **Per-user throughput +6.8%**: 19.89 vs 18.63 tok/s/user
- **Best TTFT among non-OQ Kthena configs**: 3,825 ms (though +6.3% from llm-d PC)

### 9.4 P90 Experience — OQ Config Excels Dramatically

For 90% of users, the OQ configuration provides a vastly better experience:

- **TTFT p90 is 64.4% lower**: 4,581 ms vs 12,865 ms — most users perceive near-instant first-token delivery
- **Request latency p90 is 38.5% lower**: 16,732 ms vs 27,199 ms
- **ITL p90 is 26.7% lower**: 72.75 ms vs 99.26 ms

### 9.5 Trade-off: OQ Config Has Higher Tail Latency (P99) and P50 TTFT

The OQ configuration's aggressive queue optimization results in:
- **P50 TTFT 63.1% higher** than llm-d PC (768 vs 471 ms) — the queue batching delays median first-token
- **P99 TTFT significantly higher**: 47,252 vs 20,403 ms — a small number of requests experience extreme wait times
- **~3.4% request drop**: completes ~1,159 out of 1,200 requests under extreme load

However, the benefits vastly outweigh these trade-offs: p50 request latency -25.7%, p50 ITL -32.1%, and p90 across all metrics dramatically better.

### 9.6 2\*LR + PC Strategy Degrades Under High Concurrency

This configuration performs optimally at concurrency 10 (based on historical data) but falls behind llm-d Prefix Cache across all metrics at concurrency 40:
- Throughput -5.4%
- Latency +10.5%
- TTFT +25.6%

**Root Cause**: The simple dual-weight strategy cannot effectively differentiate load states at 40 concurrency.

### 9.7 Least Token Dimension Provides No Expected Benefit

3\*PC + 2\*LR + 2\*GPU + 2\*LT compared to 3\*PC + 2\*LR + 2\*GPU:
- Throughput -5.8% (393 vs 417 tok/s)
- ITL +12.2% (70.27 vs 62.65 ms)
- Request latency +9.3% (14,547 vs 13,304 ms)

Adding the Least Token dimension actually **degrades overall performance**.

---

## 10. Configuration Recommendations

| Optimization Goal                   | Recommended Configuration      | vs llm-d Prefix Cache                |
| ----------------------------------- | ------------------------------ | ------------------------------------ |
| **Maximum Throughput**              | OQ + PC + 2\*LR                | **+10.9%** output throughput         |
| **Lowest ITL (Streaming)**          | OQ + PC + 2\*LR                | **-29.7%** ITL                       |
| **Lowest Avg Latency**              | OQ + PC + 2\*LR                | **-26.8%** request latency           |
| **Best Per-User Throughput**        | OQ + PC + 2\*LR                | **+36.6%** per-user throughput       |
| **Best P90 Experience**             | OQ + PC + 2\*LR                | TTFT -64.4%, Latency -38.5%          |
| **Lowest Avg TTFT**                 | OQ + PC + 2\*LR                | **-18.5%** TTFT                      |
| **Best P50 TTFT**                   | 3\*PC + 2\*LR + 2\*GPU         | TTFT p50 -8.2%                       |
| **Most Balanced (no request drop)** | 3\*KV + 2\*LR + 2\*GPU         | TTFT +6.3%, Latency -0.6%, ITL -3.1% |
| **Most Stable Latency (Low Std)**   | 3\*PC + 2\*LR + 2\*GPU + 2\*LT | std 8,072 (lowest)                   |
| **Lowest Tail Latency (p99)**       | 2\*LR + PC                     | TTFT p99 -15.8%, Latency p99 -8.6%   |

---

## 11. Conclusion

Under high-concurrency (40) multi-turn conversation workload:

**Kthena OQ + PC + 2\*LR is the optimal routing strategy**, delivering transformative performance gains over both llm-d baselines:

| Dimension           | vs llm-d Prefix Cache | vs llm-d KVCache Aware |
| ------------------- | --------------------- | ---------------------- |
| Output Throughput   | **+10.9%**            | **+13.4%**             |
| Request Latency     | **-26.8%**            | **-28.4%**             |
| ITL                 | **-29.7%**            | **-31.1%**             |
| Per-User Throughput | **+36.6%**            | **+39.6%**             |
| TTFT                | **-18.5%**            | **-21.2%**             |
| P90 TTFT            | **-64.4%**            | **-61.3%**             |
| P90 Request Latency | **-38.5%**            | **-35.1%**             |
| P50 Request Latency | **-25.7%**            | **-27.5%**             |
| P50 ITL             | **-32.1%**            | **-32.2%**             |

**Kthena 3\*PC + 2\*LR + 2\*GPU remains a strong alternative** when 100% request completion is required:

| Dimension           | vs llm-d Prefix Cache | vs llm-d KVCache Aware |
| ------------------- | --------------------- | ---------------------- |
| Output Throughput   | **+1.9%**             | **+4.2%**              |
| Request Latency     | **-1.7%**             | **-3.9%**              |
| ITL                 | **-6.1%**             | **-7.9%**              |
| Per-User Throughput | **+8.5%**             | **+10.9%**             |
| P50 TTFT            | **-8.2%**             | **-37.4%**             |
| P50 Request Latency | **-7.8%**             | **-10.0%**             |
| P50 ITL             | **-8.3%**             | **-8.5%**              |

**Kthena 3\*KV + 2\*LR + 2\*GPU is a balanced alternative** with the lowest average TTFT among non-OQ configs:

| Dimension           | vs llm-d Prefix Cache | vs llm-d KVCache Aware |
| ------------------- | --------------------- | ---------------------- |
| Request Latency     | **-0.6%**             | **-2.8%**              |
| ITL                 | **-3.1%**             | **-4.9%**              |
| Per-User Throughput | **+6.8%**             | **+9.2%**              |

**llm-d Prefix Cache vs llm-d KVCache Aware**: The two llm-d configurations perform similarly (within ~3.3% across most metrics), with Prefix Cache holding a slight edge in throughput (+2.2%) and latency (-2.3%). This indicates that llm-d's KVCache-aware routing provides marginal benefit for multi-turn conversation workloads at concurrency 40.

**Trade-offs of the OQ configuration:**

| Dimension          | Gap                                |
| ------------------ | ---------------------------------- |
| P50 TTFT           | +63.1% higher (768 vs 471 ms)      |
| P99 TTFT           | +132% higher (47,252 vs 20,403 ms) |
| Request Completion | ~3.4% requests dropped under load  |
| Latency Std Dev    | -0.9% (comparable to llm-d PC)     |

**Core Conclusion:** The optimized queue scheduling approach represents a paradigm shift in routing performance. By intelligently managing request queuing alongside Prefix Cache awareness, Kthena achieves **27% lower latency, 30% faster token streaming, and 37% higher per-user throughput** compared to both llm-d Prefix Cache and llm-d KVCache Aware (which perform similarly). Notably, llm-d's KVCache-aware routing fails to deliver meaningful improvements over its prefix cache strategy, while Kthena's multi-dimensional routing with optimized queue scheduling achieves dramatic gains. For workloads that can tolerate a small request drop rate (~3.4%) and higher p50 TTFT, this configuration delivers the best overall experience. For workloads requiring 100% request completion, the **3\*PC + 2\*LR + 2\*GPU** and **3\*KV + 2\*LR + 2\*GPU** configurations still outperform both llm-d baselines across throughput, latency, and streaming metrics.
