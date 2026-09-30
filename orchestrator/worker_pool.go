// Package orchestrator coordinates worker dispatch queues, concurrency limits, and ephemeral sandbox lifecycles.
package orchestrator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"axiombox/executor"
)

var (
	ErrQueueFull  = errors.New("worker dispatch queue is full; retry shortly")
	ErrPoolClosed = errors.New("orchestration pool is closed")
)

// Task represents a queued execution request with completion channel.
type Task struct {
	ID        string
	Request   executor.PipelineRequest
	ResultCh  chan TaskResult
	CreatedAt time.Time
}

// TaskResult contains the pipeline result or error.
type TaskResult struct {
	Result *executor.PipelineResult
	Err    error
}

// WorkerPool manages parallel execution pipelines bounded to the CPU thread budget.
type WorkerPool struct {
	workerCount int
	taskQueue   chan Task
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	activeTasks int64
	totalServed uint64
	closed      int32
}

// NewWorkerPool creates a worker pool sized to the target thread budget.
// For the 14C/20T Intel Core i7-12700H, 20 concurrent workers yields maximum throughput.
func NewWorkerPool(concurrency int, queueCap int) *WorkerPool {
	if concurrency <= 0 {
		concurrency = 20
	}
	if queueCap <= 0 {
		queueCap = 100
	}

	ctx, cancel := context.WithCancel(context.Background())
	wp := &WorkerPool{
		workerCount: concurrency,
		taskQueue:   make(chan Task, queueCap),
		ctx:         ctx,
		cancel:      cancel,
	}

	wp.start()
	return wp
}

func (wp *WorkerPool) start() {
	for i := 0; i < wp.workerCount; i++ {
		wp.wg.Add(1)
		go wp.workerLoop(i)
	}
}

func (wp *WorkerPool) workerLoop(id int) {
	defer wp.wg.Done()
	for {
		select {
		case <-wp.ctx.Done():
			return
		case task, ok := <-wp.taskQueue:
			if !ok {
				return
			}
			atomic.AddInt64(&wp.activeTasks, 1)
			res, err := executor.Execute(task.Request)
			atomic.AddInt64(&wp.activeTasks, -1)
			atomic.AddUint64(&wp.totalServed, 1)

			task.ResultCh <- TaskResult{Result: res, Err: err}
			close(task.ResultCh)
		}
	}
}

// Submit queues a task for asynchronous execution.
func (wp *WorkerPool) Submit(req executor.PipelineRequest) (<-chan TaskResult, error) {
	if atomic.LoadInt32(&wp.closed) == 1 {
		return nil, ErrPoolClosed
	}

	resCh := make(chan TaskResult, 1)
	task := Task{
		Request:   req,
		ResultCh:  resCh,
		CreatedAt: time.Now(),
	}

	select {
	case wp.taskQueue <- task:
		return resCh, nil
	default:
		return nil, ErrQueueFull
	}
}

// Stats returns instantaneous orchestrator health metrics.
func (wp *WorkerPool) Stats() (active int64, queueDepth int, total uint64) {
	return atomic.LoadInt64(&wp.activeTasks), len(wp.taskQueue), atomic.LoadUint64(&wp.totalServed)
}

// Shutdown gracefully stops worker routines after processing the active backlog.
func (wp *WorkerPool) Shutdown(timeout time.Duration) {
	if !atomic.CompareAndSwapInt32(&wp.closed, 0, 1) {
		return
	}
	close(wp.taskQueue)
	wp.cancel()

	done := make(chan struct{})
	go func() {
		wp.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
	}
}
