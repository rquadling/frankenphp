package frankenphp

// #include "frankenphp.h"
import "C"
import (
	"fmt"
	"sync"
)

// represents the main PHP thread
// the thread needs to keep running as long as all other threads are running
type phpMainThread struct {
	state      *threadState
	done       chan struct{}
	numThreads int
}

var (
	phpThreads []*phpThread
	mainThread *phpMainThread
)

// reserve a fixed number of PHP threads on the go side
func initPHPThreads(numThreads int) error {
	mainThread = &phpMainThread{
		state:      newThreadState(),
		done:       make(chan struct{}),
		numThreads: numThreads,
	}
	phpThreads = make([]*phpThread, numThreads)

	if err := mainThread.start(); err != nil {
		return err
	}

	// initialize all threads as inactive
	for i := 0; i < numThreads; i++ {
		phpThreads[i] = newPHPThread(i)
		convertToInactiveThread(phpThreads[i])
	}

	// start the underlying C threads
	ready := sync.WaitGroup{}
	ready.Add(numThreads)
	for _, thread := range phpThreads {
		go func() {
			if !C.frankenphp_new_php_thread(C.uintptr_t(thread.threadIndex)) {
				panic(fmt.Sprintf("unable to create thread %d", thread.threadIndex))
			}
			thread.state.waitFor(stateInactive)
			ready.Done()
		}()
	}
	ready.Wait()

	return nil
}

func drainPHPThreads() {
	doneWG := sync.WaitGroup{}
	doneWG.Add(len(phpThreads))
	for _, thread := range phpThreads {
		thread.mu.Lock()
		thread.state.set(stateShuttingDown)
		close(thread.drainChan)
	}
	close(mainThread.done)
	for _, thread := range phpThreads {
		go func(thread *phpThread) {
			thread.state.waitFor(stateDone)
			thread.mu.Unlock()
			doneWG.Done()
		}(thread)
	}
	doneWG.Wait()
	mainThread.state.set(stateShuttingDown)
	mainThread.state.waitFor(stateDone)
	phpThreads = nil
}

func (mainThread *phpMainThread) start() error {
	if C.frankenphp_new_main_thread(C.int(mainThread.numThreads)) != 0 {
		return MainThreadCreationError
	}
	mainThread.state.waitFor(stateReady)
	return nil
}

func getInactivePHPThread() *phpThread {
	for _, thread := range phpThreads {
		if thread.state.is(stateInactive) {
			return thread
		}
	}
	panic("not enough threads reserved")
}

//export go_frankenphp_main_thread_is_ready
func go_frankenphp_main_thread_is_ready() {
	mainThread.state.set(stateReady)
	mainThread.state.waitFor(stateShuttingDown)
}

//export go_frankenphp_shutdown_main_thread
func go_frankenphp_shutdown_main_thread() {
	mainThread.state.set(stateDone)
}
