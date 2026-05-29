1. gsp-num-groups: 64, gsp-system-prompt-len: 6400, gsp-question-len: 256, gsp-output-len: 256, max-concurrency: 16

1.1 sequential:

============ Serving Benchmark Result ============
Backend:                                 vllm      
Traffic request rate:                    800.0     
Max request concurrency:                 16        
Successful requests:                     1024      
Benchmark duration (s):                  225.35    
Total input tokens:                      7175676   
Total generated tokens:                  262144    
Total generated tokens (retokenized):    147010    
Request throughput (req/s):              4.54      
Input token throughput (tok/s):          31842.88  
Output token throughput (tok/s):         1163.29   
Total token throughput (tok/s):          33006.18  
Concurrency:                             15.99     
----------------End-to-End Latency----------------
Mean E2E Latency (ms):                   3518.88   
Median E2E Latency (ms):                 3504.26   
---------------Time to First Token----------------
Mean TTFT (ms):                          398.89    
Median TTFT (ms):                        415.22    
P99 TTFT (ms):                           508.51    
---------------Inter-Token Latency----------------
Mean ITL (ms):                           12.29     
Median ITL (ms):                         11.87     
P95 ITL (ms):                            13.96     
P99 ITL (ms):                            22.60     
Max ITL (ms):                            170.93    
==================================================

2.2 interleaved:

============ Serving Benchmark Result ============
Backend:                                 vllm      
Traffic request rate:                    800.0     
Max request concurrency:                 16        
Successful requests:                     1024      
Benchmark duration (s):                  961.88    
Total input tokens:                      7170704   
Total generated tokens:                  262144    
Total generated tokens (retokenized):    136477    
Request throughput (req/s):              1.06      
Input token throughput (tok/s):          7454.91   
Output token throughput (tok/s):         272.53    
Total token throughput (tok/s):          7727.45   
Concurrency:                             15.99     
----------------End-to-End Latency----------------
Mean E2E Latency (ms):                   15020.89  
Median E2E Latency (ms):                 15035.76  
---------------Time to First Token----------------
Mean TTFT (ms):                          670.18    
Median TTFT (ms):                        649.81    
P99 TTFT (ms):                           1250.97   
---------------Inter-Token Latency----------------
Mean ITL (ms):                           56.58     
Median ITL (ms):                         44.15     
P95 ITL (ms):                            112.38    
P99 ITL (ms):                            116.78    
Max ITL (ms):                            233.17    
==================================================


2. gsp-num-groups: 64, gsp-system-prompt-len: 6400, gsp-question-len: 256, gsp-output-len: 256, max-concurrency: 4

2.1 sequential:

============ Serving Benchmark Result ============
Backend:                                 vllm      
Traffic request rate:                    800.0     
Max request concurrency:                 4         
Successful requests:                     1024      
Benchmark duration (s):                  615.70    
Total input tokens:                      7177904   
Total generated tokens:                  262144    
Total generated tokens (retokenized):    142082    
Request throughput (req/s):              1.66      
Input token throughput (tok/s):          11658.07  
Output token throughput (tok/s):         425.76    
Total token throughput (tok/s):          12083.83  
Concurrency:                             4.00      
----------------End-to-End Latency----------------
Mean E2E Latency (ms):                   2404.24   
Median E2E Latency (ms):                 2355.21   
---------------Time to First Token----------------
Mean TTFT (ms):                          105.42    
Median TTFT (ms):                        79.91     
P99 TTFT (ms):                           313.94    
---------------Inter-Token Latency----------------
Mean ITL (ms):                           9.06      
Median ITL (ms):                         8.82      
P95 ITL (ms):                            10.49     
P99 ITL (ms):                            17.58     
Max ITL (ms):                            90.34     
==================================================

2.2 interleaved:

============ Serving Benchmark Result ============
Backend:                                 vllm      
Traffic request rate:                    800.0     
Max request concurrency:                 4         
Successful requests:                     1024      
Benchmark duration (s):                  1163.99   
Total input tokens:                      7176304   
Total generated tokens:                  262144    
Total generated tokens (retokenized):    146479    
Request throughput (req/s):              0.88      
Input token throughput (tok/s):          6165.24   
Output token throughput (tok/s):         225.21    
Total token throughput (tok/s):          6390.45   
Concurrency:                             4.00      
----------------End-to-End Latency----------------
Mean E2E Latency (ms):                   4546.10   
Median E2E Latency (ms):                 4547.10   
---------------Time to First Token----------------
Mean TTFT (ms):                          501.68    
Median TTFT (ms):                        515.92    
P99 TTFT (ms):                           654.41    
---------------Inter-Token Latency----------------
Mean ITL (ms):                           15.98     
Median ITL (ms):                         14.34     
P95 ITL (ms):                            16.62     
P99 ITL (ms):                            72.74     
Max ITL (ms):                            280.32    
==================================================


