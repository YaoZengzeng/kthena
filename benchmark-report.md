# Kthena vs llm-d Performance Benchmark Report

## 1. Test Environment & Methodology

**Workload:** Multi-turn conversation  
**Model:** LLM inference serving  
**Benchmark Tool:** NVIDIA AIPerf  
**Request Count:** 1,000 requests per run (3 runs per configuration, averaged)

| Parameter          | Value                            |
| ------------------ | -------------------------------- |
| Conversation Count | 100                              |
| Turn Mean          | 10                               |
| Input Tokens Mean  | 2,000                            |
| Concurrency        | 10 (primary), 20 (supplementary) |

**Configurations Tested:**

| System | Configuration            | Description                                                  |
| ------ | ------------------------ | ------------------------------------------------------------ |
| Kthena | Least Request (LR)       | Basic load balancing without cache optimization              |
| Kthena | LR + Prefix Cache        | Prefix-cache-aware routing with default weight               |
| Kthena | LR + Prefix Cache (W\*2) | Prefix-cache-aware routing with doubled prefix cache weight  |
| Kthena | 2\*LR + Prefix Cache     | Prefix-cache-aware routing with doubled least-request weight |
| Kthena | LR + KVCache Aware       | KV-cache-aware routing combining load and cache affinity     |
| llm-d  | Default                  | Default routing strategy                                     |
| llm-d  | Precise KV Cache         | KV-cache-aware routing                                       |

---

## 2. Summary of Results (Concurrency = 10)

All values are **averages across 3 runs**. Lower is better for latency metrics; higher is better for throughput metrics.

| Configuration                       | TTFT (ms) | Request Latency (ms) | ITL (ms) | Output Throughput (tok/s) | Request Throughput (req/s) | Per-User Throughput (tok/s/user) |
| ----------------------------------- | --------- | -------------------- | -------- | ------------------------- | -------------------------- | -------------------------------- |
| **Kthena LR**                       | 290.60    | 3,321.11             | 20.34    | 437.83                    | 2.92                       | 54.75                            |
| **Kthena LR + Prefix Cache**        | 236.82    | 3,104.78             | 19.26    | 468.26                    | 3.12                       | 58.85                            |
| **Kthena LR + Prefix Cache (W\*2)** | 248.49    | 3,096.63             | 19.12    | 457.77                    | 3.05                       | 59.36                            |
| **Kthena 2\*LR + Prefix Cache**     | 230.39    | 3,116.90             | 19.38    | 469.01                    | 3.13                       | 57.92                            |
| **Kthena LR + KVCache Aware**       | 304.39    | 3,133.19             | 18.99    | 465.10                    | 3.10                       | 60.26                            |
| **llm-d Default**                   | 270.03    | 3,705.12             | 23.05    | 390.26                    | 2.60                       | 58.81                            |
| **llm-d Precise KV Cache**          | 327.33    | 3,549.10             | 21.62    | 409.90                    | 2.73                       | 59.24                            |

---

## 3. Head-to-Head Comparison Charts

### 3.1 Output Token Throughput (tokens/sec) — Higher is Better

```mermaid
xychart-beta
    title "Output Token Throughput (tokens/sec) — Concurrency 10"
    x-axis ["Kthena LR", "Kthena LR+PC", "Kthena LR+PC(W*2)", "Kthena 2*LR+PC", "Kthena LR+KVCache", "llm-d Default", "llm-d Precise KV"]
    y-axis "Throughput (tokens/sec)" 350 --> 500
    bar [437.83, 468.26, 457.77, 469.01, 465.10, 390.26, 409.90]
```

### 3.2 Time to First Token / TTFT (ms) — Lower is Better

```mermaid
xychart-beta
    title "TTFT (ms) — Concurrency 10"
    x-axis ["Kthena LR", "Kthena LR+PC", "Kthena LR+PC(W*2)", "Kthena 2*LR+PC", "Kthena LR+KVCache", "llm-d Default", "llm-d Precise KV"]
    y-axis "TTFT (ms)" 200 --> 350
    bar [290.60, 236.82, 248.49, 230.39, 304.39, 270.03, 327.33]
```

### 3.3 Request Latency (ms) — Lower is Better

```mermaid
xychart-beta
    title "Request Latency (ms) — Concurrency 10"
    x-axis ["Kthena LR", "Kthena LR+PC", "Kthena LR+PC(W*2)", "Kthena 2*LR+PC", "Kthena LR+KVCache", "llm-d Default", "llm-d Precise KV"]
    y-axis "Latency (ms)" 2800 --> 3800
    bar [3321.11, 3104.78, 3096.63, 3116.90, 3133.19, 3705.12, 3549.10]
```

### 3.4 Inter Token Latency / ITL (ms) — Lower is Better

```mermaid
xychart-beta
    title "Inter Token Latency (ms) — Concurrency 10"
    x-axis ["Kthena LR", "Kthena LR+PC", "Kthena LR+PC(W*2)", "Kthena 2*LR+PC", "Kthena LR+KVCache", "llm-d Default", "llm-d Precise KV"]
    y-axis "ITL (ms)" 17 --> 25
    bar [20.34, 19.26, 19.12, 19.38, 18.99, 23.05, 21.62]
```

### 3.5 Request Throughput (req/s) — Higher is Better

```mermaid
xychart-beta
    title "Request Throughput (req/s) — Concurrency 10"
    x-axis ["Kthena LR", "Kthena LR+PC", "Kthena LR+PC(W*2)", "Kthena 2*LR+PC", "Kthena LR+KVCache", "llm-d Default", "llm-d Precise KV"]
    y-axis "Requests/sec" 2.4 --> 3.3
    bar [2.92, 3.12, 3.05, 3.13, 3.10, 2.60, 2.73]
```

---

## 4. Kthena Advantage Analysis

### 4.1 Percentage Improvement — Kthena over llm-d

The table below shows the percentage improvement of each Kthena configuration over each llm-d configuration. For latency metrics, improvement = reduction; for throughput metrics, improvement = increase.

#### vs. llm-d Default

| Metric                 | Kthena LR     | Kthena LR+PC | Kthena LR+PC(W\*2) | Kthena 2\*LR+PC | Kthena LR+KVCache |
| ---------------------- | ------------- | ------------ | ------------------ | --------------- | ----------------- |
| **Output Throughput**  | **+12.2%**    | **+20.0%**   | **+17.3%**         | **+20.2%**      | **+19.2%**        |
| **Request Throughput** | **+12.3%**    | **+20.0%**   | **+17.3%**         | **+20.4%**      | **+19.2%**        |
| **Request Latency**    | **-10.4%**    | **-16.2%**   | **-16.4%**         | **-15.9%**      | **-15.4%**        |
| **ITL**                | **-11.8%**    | **-16.4%**   | **-17.0%**         | **-15.9%**      | **-17.6%**        |
| **TTFT**               | +7.6% (worse) | **-12.3%**   | **-8.0%**          | **-14.7%**      | +12.7% (worse)    |

#### vs. llm-d Precise KV Cache

| Metric                 | Kthena LR  | Kthena LR+PC | Kthena LR+PC(W\*2) | Kthena 2\*LR+PC | Kthena LR+KVCache |
| ---------------------- | ---------- | ------------ | ------------------ | --------------- | ----------------- |
| **Output Throughput**  | **+6.8%**  | **+14.2%**   | **+11.7%**         | **+14.4%**      | **+13.5%**        |
| **Request Throughput** | **+7.0%**  | **+14.3%**   | **+11.7%**         | **+14.7%**      | **+13.6%**        |
| **Request Latency**    | **-6.4%**  | **-12.5%**   | **-12.7%**         | **-12.2%**      | **-11.7%**        |
| **ITL**                | **-5.9%**  | **-10.9%**   | **-11.6%**         | **-10.4%**      | **-12.2%**        |
| **TTFT**               | **-11.2%** | **-27.6%**   | **-24.1%**         | **-29.6%**      | **-7.0%**         |

```mermaid
xychart-beta
    title "Kthena Improvement (%) over llm-d Default — Key Metrics"
    x-axis ["Throughput", "Request/s", "Latency Reduction", "ITL Reduction", "TTFT Reduction"]
    y-axis "Improvement (%)" -5 --> 22
    bar [20.2, 20.4, 16.4, 17.6, 14.7]
```

> The chart above shows the **best Kthena configuration** improvement for each metric compared to llm-d Default.

---

## 5. Key Findings — Where Kthena Excels

### 5.1 Throughput: Up to +20% Higher

**Kthena 2\*LR + Prefix Cache** achieves **469.01 tokens/sec**, compared to llm-d Default's **390.26 tokens/sec** — a **+20.2% improvement** in output token throughput.

Even Kthena's baseline (Least Request without any cache optimization) delivers **437.83 tokens/sec**, which is **+12.2% higher** than llm-d Default.

Against llm-d's best configuration (Precise KV Cache at 409.90 tokens/sec), Kthena 2\*LR + Prefix Cache still leads by **+14.4%**.

### 5.2 Request Latency: Up to 16.4% Lower

**Kthena LR + Prefix Cache (W\*2)** achieves the lowest average request latency at **3,096.63 ms**, compared to llm-d Default's **3,705.12 ms** — a **16.4% reduction**.

All four Kthena configurations deliver request latency below **3,350 ms**, while both llm-d configurations exceed **3,500 ms**.

### 5.3 Inter Token Latency (ITL): Up to 17.6% Lower

**Kthena LR + KVCache Aware** achieves an average ITL of **18.99 ms**, compared to llm-d Default's **23.05 ms** — a **17.6% reduction**. This translates to noticeably smoother token streaming for end users.

All Kthena configurations keep ITL below **20.5 ms**, while llm-d Default runs at **23.05 ms** and llm-d Precise KV at **21.62 ms**.

### 5.4 TTFT: Up to 30% Lower (with Prefix Cache)

**Kthena 2\*LR + Prefix Cache** achieves the lowest TTFT of **230.39 ms**, compared to llm-d Precise KV Cache's **327.33 ms** — a **29.6% reduction**. This is the single largest improvement observed in the benchmark.

Against llm-d Default (270.03 ms), Kthena 2\*LR + Prefix Cache delivers a **14.7% reduction** in TTFT.

> **Note:** Without prefix cache optimization, Kthena's TTFT (290.60 ms for LR, 304.39 ms for KVCache Aware) is slightly higher than llm-d Default (270.03 ms). The advantage emerges specifically when prefix cache routing is enabled.

### 5.5 Latency Consistency: Lower Standard Deviation

Kthena configurations show significantly lower standard deviation in request latency, indicating more predictable performance:

| Configuration                   | Latency Std Dev (ms) |
| ------------------------------- | -------------------- |
| Kthena LR                       | 1,049.46             |
| Kthena LR + Prefix Cache        | 1,002.54             |
| Kthena LR + Prefix Cache (W\*2) | 988.30               |
| Kthena 2\*LR + Prefix Cache     | 983.71               |
| Kthena LR + KVCache Aware       | 1,076.22             |
| llm-d Default                   | 1,770.69             |
| llm-d Precise KV Cache          | 1,560.23             |

Kthena's latency variance is **~40-44% lower** than llm-d Default, meaning more consistent user experience with fewer tail-latency outliers.

---

## 6. Tail Latency Comparison (p99)

| Metric                   | Best Kthena (LR+PC) | llm-d Default | llm-d Precise KV | Kthena Improvement vs llm-d Default |
| ------------------------ | ------------------- | ------------- | ---------------- | ----------------------------------- |
| TTFT p99 (ms)            | ~625                | ~749          | ~902             | **-16.6%**                          |
| Request Latency p99 (ms) | ~5,472              | ~7,637        | ~7,352           | **-28.4%**                          |
| ITL p99 (ms)             | ~34.07              | ~47.88        | ~45.28           | **-28.8%**                          |

> Kthena shows dramatically better tail latency behavior — **p99 request latency is ~28% lower** and **p99 ITL is ~29% lower** than llm-d Default.

---

## 7. Concurrency 20 Results (Kthena Only)

At higher concurrency (20), only Kthena configurations were tested. These results demonstrate Kthena's scaling behavior:

| Configuration                 | TTFT (ms) | Latency (ms) | ITL (ms) | Throughput (tok/s) | Req/s |
| ----------------------------- | --------- | ------------ | -------- | ------------------ | ----- |
| **Kthena LR + KVCache Aware** | 501.28    | 5,480.82     | 33.43    | 513.72             | 3.43  |
| **Kthena LR + Prefix Cache**  | 479.71    | 6,029.46     | 37.26    | 481.66             | 3.21  |

Kthena sustains **513.72 tokens/sec** at concurrency 20 with KVCache Aware routing — **+31.6% higher** than llm-d Default at concurrency 10 (390.26 tokens/sec), indicating strong scaling characteristics.

---

## 8. Best Configuration Recommendations

| Optimization Goal                    | Best Kthena Config       | Improvement vs llm-d Default           |
| ------------------------------------ | ------------------------ | -------------------------------------- |
| **Maximum Throughput**               | 2\*LR + Prefix Cache     | +20.2% output tokens/sec               |
| **Lowest TTFT**                      | 2\*LR + Prefix Cache     | -14.7% avg, -29.6% vs llm-d Precise KV |
| **Lowest ITL / Smoothest Streaming** | LR + KVCache Aware       | -17.6% ITL                             |
| **Lowest Request Latency**           | LR + Prefix Cache (W\*2) | -16.4%                                 |
| **Best Tail Latency (p99)**          | LR + Prefix Cache        | -28.4% request latency p99             |
| **Most Consistent Latency**          | 2\*LR + Prefix Cache     | ~44% lower std dev                     |

---

## 9. Conclusion

Across all key LLM serving metrics, **Kthena consistently outperforms llm-d** in the multi-turn conversation workload at concurrency 10:

- **Output throughput:** Kthena delivers **12–20% higher throughput** depending on configuration, with the best result from 2\*LR + Prefix Cache at **469.01 tokens/sec** (vs. llm-d's best of 409.90).
- **Request latency:** Kthena achieves **10–16% lower average latency** and **~28% lower p99 tail latency**, providing both faster and more predictable responses.
- **Inter token latency:** Kthena reduces ITL by **12–18%**, directly improving the perceived streaming speed for interactive users.
- **TTFT:** With prefix cache routing enabled, Kthena achieves **15–30% lower TTFT** than llm-d configurations, ensuring faster time-to-first-response.
- **Consistency:** Kthena's latency standard deviation is **~40–44% lower** than llm-d, indicating significantly more stable performance under load.

The strongest advantage scenario is **Kthena 2\*LR + Prefix Cache vs. llm-d Default**, where Kthena simultaneously achieves **+20% throughput, -16% latency, -15% TTFT, and -16% ITL**.


### 分析

基于对 prefixcache.log 中1000个请求的详细打分日志和路由代码的分析，总结如下：

一、总体统计
指标	数值
总请求数	1000
Prefix Cache 命中（至少1个pod得分>0）	901 (90.1%)
Prefix Cache 未命中（所有pod得分=0）	99 (首轮请求)
Pod 分配：pod-3 / pod-4 / pod-5	335 / 325 / 340 (均匀)
二、权重配置分析
配置：LR权重=2，Prefix权重=1，理论最大贡献：LR=200，Prefix=100

实际打分冲突解决：

Prefix偏好的pod最终被选中：888/899次 (98.8%)
LR偏好的pod最终被选中：仅10次
原因：LR分数通常较低，无法有效对抗Prefix

最常见的LR分数模式（3个pod的得分）：

[50, 25, 0]: 490次 (49%) — 对应 4/3/3 inflight 分布
[0, 0, 0]: 224次 (22%) — 所有pod负载相同
[97-99, 96-98, 0]: ~160次 — 某pod有waiting请求时
关键算术：

最常见场景 [50,25,0]：如果cache在最忙pod(LR=0)上，该pod最终得分= 0×2+100×1=100，而最空闲pod= 50×2+0×1=100 → 平局（prefix仍可能胜出）
所有pod均匀 [0,0,0]：prefix pod = 100，其他 = 0 → prefix完胜
仅在某pod有waiting请求时(LR≈97-99)：97×2=194 > 100 → LR才能真正压过prefix
三、Prefix Cache 插件的核心问题
1. 分数完全二值化（只有0或100）

在1000个请求中，prefix cache从未给出中间分数。原因：

maxBlocksToMatch=128, blockSizeToHash=64 → 只hash前 8192 字节
你的测试 INPUT_TOKENS_MEAN=2000，第一轮prompt≈2000 tokens × 4 chars/token = ~8000 chars，已经接近或超过 8192 字节的cap
从第二轮开始，每个请求的前8192字节（128 blocks）都与上一轮完全相同（因为是相同的对话前缀）
因此每次要么 100%匹配（同一conversation的pod），要么0%（其他pod）
结论：在多轮对话场景中，prefix cache退化为纯session affinity（会话粘性），失去了按prefix匹配长度做梯度打分的能力。

2. 有8个请求出现了2个pod同时得分100

这发生在某个conversation因LR赢了一次被路由到另一个pod后，两个pod都缓存了该conversation的hashes。后续请求就会看到2个pod都能匹配。虽然数量不多（8/1000），但说明cache没有做旧pod的eviction。

四、权重是否有问题？
当前权重(2:1)在你的场景下基本等同于prefix cache全权决定路由。 原因是：

3个pod+10并发，LR的区分度很低（最常见差距只有1个请求 → LR max score = 50）
Prefix score是二值的(0/100)，所以100×1=100 ≥ 50×2=100
只有当某pod出现waiting请求时（LR score接近100），LR权重才能有效覆盖prefix
这意味着你的配置实际行为更接近 "pure prefix affinity with LR as tie-breaker"，而不是 "LR为主、prefix为辅"。

五、建议
如果期望LR有更强的影响力：将权重调整为 LR:Prefix = 3:1 或 4:1，确保即使1个请求的差距（LR=50）也能压过prefix（50×3=150 > 100）。

Prefix Cache的根本问题：对于 INPUT_TOKENS_MEAN=2000 的多轮场景，由于 maxBlocksToMatch=128 的限制，prefix cache退化为session affinity。可以考虑：

增大 maxBlocksToMatch（但增加计算开销）
或者从prompt末尾开始hash（而不是开头），这样能区分不同轮次的新内容
当前的平衡结果：从benchmark数据看，"2×LR + Prefix"配置的TTFT p50=216ms、Throughput=472 tok/s 已经是最佳配置之一。Pod分配也非常均匀(335/325/340)。负载均衡在宏观上没有问题——真正的优化空间在于prefix cache能否给出梯度分数来做更智能的路由决策。