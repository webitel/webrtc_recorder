package utils

import (
	"context"
	"sync"
)

type Task interface {
	Execute()
}

type Pool struct {
	mu    sync.Mutex
	size  int
	tasks chan Task
	kill  chan struct{}
	wg    sync.WaitGroup
	ctx   context.Context
}

func NewPool(ctx context.Context, workers int, queueCount int) *Pool {
	pool := &Pool{
		tasks: make(chan Task, queueCount),
		kill:  make(chan struct{}),
		ctx:   ctx,
	}
	pool.Resize(workers)
	return pool
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for {
		select {
		case task, ok := <-p.tasks:
			if !ok {
				return
			}
			task.Execute()
		case <-p.ctx.Done():
			return
		case <-p.kill:
			return
		}
	}
}

func (p *Pool) Resize(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.size < n {
		p.size++
		p.wg.Add(1)
		go p.worker()
	}
	for p.size > n {
		p.size--
		p.kill <- struct{}{}
	}
}

func (p *Pool) Close() {
	close(p.tasks)
}

func (p *Pool) Wait() {
	p.wg.Wait()
}

func (p *Pool) Exec(task Task) {
	p.tasks <- task
}

func (p *Pool) ChannelJobs() chan Task {
	return p.tasks
}
