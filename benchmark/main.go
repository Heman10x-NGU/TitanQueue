package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hemant/titanqueue"
	"github.com/redis/go-redis/v9"
)

const redisAddr = "localhost:6379"

type BenchmarkResult struct {
	Name     string
	Tasks    int
	Workers  int
	Duration time.Duration
	Rate     float64
	RateK    float64
	Success  int64
	Failed   int64
}

var allResults []BenchmarkResult

func clearRedis() {
	client := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	defer client.Close()
	client.FlushAll(context.Background())
}

// BenchmarkEnqueue tests raw enqueue throughput
func BenchmarkEnqueue(numTasks int, concurrency int) BenchmarkResult {
	log.Printf("\n=== ENQUEUE BENCHMARK ===")
	log.Printf("Tasks: %d, Concurrency: %d goroutines", numTasks, concurrency)

	client := titanqueue.NewClient(titanqueue.RedisClientOpt{
		Addr: redisAddr,
	})
	defer client.Close()

	payload, _ := json.Marshal(map[string]interface{}{
		"task_id":   0,
		"data":      "benchmark payload data for testing throughput",
		"timestamp": time.Now().Unix(),
	})

	var wg sync.WaitGroup
	var successCount int64
	var failCount int64

	tasksPerWorker := numTasks / concurrency
	start := time.Now()

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < tasksPerWorker; i++ {
				task := titanqueue.NewTask("benchmark:task", payload)
				_, err := client.Enqueue(task, titanqueue.Queue("default"))
				if err != nil {
					atomic.AddInt64(&failCount, 1)
				} else {
					atomic.AddInt64(&successCount, 1)
				}
			}
		}(w)
	}

	wg.Wait()
	duration := time.Since(start)

	rate := float64(successCount) / duration.Seconds()
	result := BenchmarkResult{
		Name:     fmt.Sprintf("Enqueue (concurrency=%d)", concurrency),
		Tasks:    numTasks,
		Workers:  concurrency,
		Duration: duration,
		Rate:     rate,
		RateK:    rate / 1000,
		Success:  successCount,
		Failed:   failCount,
	}

	log.Printf("Results:")
	log.Printf("  Duration: %v", duration)
	log.Printf("  Success: %d, Failed: %d", successCount, failCount)
	log.Printf("  Enqueue Rate: %.2f tasks/sec", rate)
	log.Printf("  Rate (K): %.2f K tasks/sec", rate/1000)

	return result
}

// BenchmarkProcessing tests task processing throughput
func BenchmarkProcessing(numTasks int, workers int) BenchmarkResult {
	log.Printf("\n=== PROCESSING BENCHMARK ===")
	log.Printf("Tasks: %d, Worker Pool: %d workers", numTasks, workers)

	// First, enqueue all tasks
	log.Println("Pre-enqueueing tasks...")
	client := titanqueue.NewClient(titanqueue.RedisClientOpt{
		Addr: redisAddr,
	})

	payload, _ := json.Marshal(map[string]interface{}{
		"task_id": 0,
		"data":    "benchmark",
	})

	var wg sync.WaitGroup
	enqueueWorkers := 100
	tasksPerWorker := numTasks / enqueueWorkers

	for w := 0; w < enqueueWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < tasksPerWorker; i++ {
				task := titanqueue.NewTask("benchmark:process", payload)
				client.Enqueue(task, titanqueue.Queue("default"))
			}
		}()
	}
	wg.Wait()
	client.Close()
	log.Printf("Pre-enqueued %d tasks", numTasks)

	// Now process them
	var processedCount int64
	var startTime time.Time
	var started bool
	var mu sync.Mutex

	srv := titanqueue.NewServer(
		titanqueue.RedisClientOpt{Addr: redisAddr},
		titanqueue.Config{
			Concurrency: workers,
			Queues: map[string]int{
				"default": 1,
			},
		},
	)

	handler := titanqueue.HandlerFunc(func(ctx context.Context, task *titanqueue.Task) error {
		mu.Lock()
		if !started {
			startTime = time.Now()
			started = true
		}
		mu.Unlock()
		atomic.AddInt64(&processedCount, 1)
		return nil
	})

	// Run server in background
	go func() {
		if err := srv.Run(handler); err != nil {
			log.Printf("Server error: %v", err)
		}
	}()

	// Wait for processing to complete or timeout
	timeout := time.After(120 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var result BenchmarkResult

	for {
		select {
		case <-ticker.C:
			count := atomic.LoadInt64(&processedCount)
			if count >= int64(numTasks) {
				duration := time.Since(startTime)
				rate := float64(count) / duration.Seconds()
				result = BenchmarkResult{
					Name:     fmt.Sprintf("Processing (workers=%d)", workers),
					Tasks:    numTasks,
					Workers:  workers,
					Duration: duration,
					Rate:     rate,
					RateK:    rate / 1000,
					Success:  count,
					Failed:   0,
				}
				log.Printf("Results:")
				log.Printf("  Duration: %v", duration)
				log.Printf("  Processed: %d tasks", count)
				log.Printf("  Processing Rate: %.2f tasks/sec", rate)
				log.Printf("  Rate (K): %.2f K tasks/sec", rate/1000)
				srv.Shutdown()
				return result
			}
		case <-timeout:
			count := atomic.LoadInt64(&processedCount)
			duration := time.Since(startTime)
			rate := float64(count) / duration.Seconds()
			result = BenchmarkResult{
				Name:     fmt.Sprintf("Processing (workers=%d)", workers),
				Tasks:    numTasks,
				Workers:  workers,
				Duration: duration,
				Rate:     rate,
				RateK:    rate / 1000,
				Success:  count,
				Failed:   int64(numTasks) - count,
			}
			log.Printf("TIMEOUT - Results so far:")
			log.Printf("  Duration: %v", duration)
			log.Printf("  Processed: %d tasks", count)
			log.Printf("  Processing Rate: %.2f tasks/sec", rate)
			log.Printf("  Rate (K): %.2f K tasks/sec", rate/1000)
			srv.Shutdown()
			return result
		}
	}
}

// BenchmarkMixedLoad tests combined enqueue + processing throughput
func BenchmarkMixedLoad(duration time.Duration, enqueueWorkers, processWorkers int) (BenchmarkResult, BenchmarkResult) {
	log.Printf("\n=== MIXED LOAD BENCHMARK ===")
	log.Printf("Duration: %v, Enqueue Workers: %d, Process Workers: %d", duration, enqueueWorkers, processWorkers)

	// Start server first
	var processedCount int64
	srv := titanqueue.NewServer(
		titanqueue.RedisClientOpt{Addr: redisAddr},
		titanqueue.Config{
			Concurrency: processWorkers,
			Queues: map[string]int{
				"default": 1,
			},
		},
	)

	handler := titanqueue.HandlerFunc(func(ctx context.Context, task *titanqueue.Task) error {
		atomic.AddInt64(&processedCount, 1)
		return nil
	})

	go func() {
		if err := srv.Run(handler); err != nil {
			log.Printf("Server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Start enqueueing
	var enqueuedCount int64
	stopEnqueue := make(chan struct{})

	client := titanqueue.NewClient(titanqueue.RedisClientOpt{
		Addr: redisAddr,
	})

	payload, _ := json.Marshal(map[string]interface{}{"data": "mixed load test"})

	for w := 0; w < enqueueWorkers; w++ {
		go func() {
			for {
				select {
				case <-stopEnqueue:
					return
				default:
					task := titanqueue.NewTask("benchmark:mixed", payload)
					_, err := client.Enqueue(task, titanqueue.Queue("default"))
					if err == nil {
						atomic.AddInt64(&enqueuedCount, 1)
					}
				}
			}
		}()
	}

	start := time.Now()
	time.Sleep(duration)
	close(stopEnqueue)
	elapsed := time.Since(start)

	// Wait a bit for remaining tasks to process
	time.Sleep(2 * time.Second)

	enqueued := atomic.LoadInt64(&enqueuedCount)
	processed := atomic.LoadInt64(&processedCount)

	enqueueRate := float64(enqueued) / elapsed.Seconds()
	processRate := float64(processed) / elapsed.Seconds()

	log.Printf("Results:")
	log.Printf("  Duration: %v", elapsed)
	log.Printf("  Enqueued: %d tasks", enqueued)
	log.Printf("  Processed: %d tasks", processed)
	log.Printf("  Enqueue Rate: %.2f tasks/sec (%.2f K/sec)", enqueueRate, enqueueRate/1000)
	log.Printf("  Process Rate: %.2f tasks/sec (%.2f K/sec)", processRate, processRate/1000)

	client.Close()
	srv.Shutdown()

	enqueueResult := BenchmarkResult{
		Name:     fmt.Sprintf("Mixed Enqueue (workers=%d)", enqueueWorkers),
		Tasks:    int(enqueued),
		Workers:  enqueueWorkers,
		Duration: elapsed,
		Rate:     enqueueRate,
		RateK:    enqueueRate / 1000,
		Success:  enqueued,
		Failed:   0,
	}

	processResult := BenchmarkResult{
		Name:     fmt.Sprintf("Mixed Process (workers=%d)", processWorkers),
		Tasks:    int(processed),
		Workers:  processWorkers,
		Duration: elapsed,
		Rate:     processRate,
		RateK:    processRate / 1000,
		Success:  processed,
		Failed:   0,
	}

	return enqueueResult, processResult
}

func printSummaryTable() {
	fmt.Println("\n╔══════════════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                           BENCHMARK RESULTS SUMMARY                                   ║")
	fmt.Println("╠═══════════════════════════════════════════════╦═══════════╦═══════════╦══════════════╣")
	fmt.Println("║ Test                                          ║  Tasks    ║  Workers  ║  Rate (K/s)  ║")
	fmt.Println("╠═══════════════════════════════════════════════╬═══════════╬═══════════╬══════════════╣")

	for _, r := range allResults {
		fmt.Printf("║ %-45s ║ %9d ║ %9d ║ %10.2f K ║\n", r.Name, r.Tasks, r.Workers, r.RateK)
	}

	fmt.Println("╚═══════════════════════════════════════════════╩═══════════╩═══════════╩══════════════╝")
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	fmt.Println("╔══════════════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                        TITANQUEUE BENCHMARK SUITE                                    ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════════════════════════╝")
	log.Printf("CPU Cores: %d | GOMAXPROCS: %d", runtime.NumCPU(), runtime.GOMAXPROCS(0))
	log.Printf("Started at: %s", time.Now().Format("2006-01-02 15:04:05"))

	// Test 1: Pure Enqueue Performance (various concurrency levels)
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("                              ENQUEUE BENCHMARKS")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	for _, concurrency := range []int{10, 50, 100, 200} {
		clearRedis()
		result := BenchmarkEnqueue(100000, concurrency)
		allResults = append(allResults, result)
	}

	// Test 2: Processing Performance (various worker counts)
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("                            PROCESSING BENCHMARKS")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	for _, workers := range []int{10, 25, 50, 100} {
		clearRedis()
		result := BenchmarkProcessing(50000, workers)
		allResults = append(allResults, result)
	}

	// Test 3: Mixed Load (enqueue + process together)
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("                             MIXED LOAD BENCHMARKS")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	clearRedis()
	enqResult, procResult := BenchmarkMixedLoad(10*time.Second, 50, 50)
	allResults = append(allResults, enqResult, procResult)

	// Final comprehensive test
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("                          FINAL VERIFICATION TEST")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	clearRedis()
	finalEnqueue := BenchmarkEnqueue(200000, 100)
	allResults = append(allResults, finalEnqueue)

	clearRedis()
	finalProcess := BenchmarkProcessing(100000, 50)
	allResults = append(allResults, finalProcess)

	// Print summary
	printSummaryTable()

	log.Printf("\nCompleted at: %s", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println("\n✅ Benchmark complete!")
}
