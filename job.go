package fan

import (
	"sync"
)

type jobsheet struct {
	result            error
	resultIndex       int
	resultMutex       sync.RWMutex
	workCount         int
	addedWorkCount    chan int
	pendingWorkload   chan *worksheet
	completedWorkload chan *worksheet
	stopWork          chan struct{}
}

const (
	jobsheetWorkloadMaxBufferSize = 1e6
)

func newJobsheet(workCount int) *jobsheet {
	workloadBufferSize := workCount

	if workloadBufferSize > jobsheetWorkloadMaxBufferSize {
		workloadBufferSize = jobsheetWorkloadMaxBufferSize
	}

	return &jobsheet{
		workCount:         workCount,
		pendingWorkload:   make(chan *worksheet, workloadBufferSize),
		completedWorkload: make(chan *worksheet, workloadBufferSize),
		stopWork:          make(chan struct{}),
		addedWorkCount:    make(chan int, 1),
	}
}

func (j *jobsheet) addOne(handler Handler, index int) (wasWorthStarting bool) {
	if j.isWorthStarting(index) {
		j.pendingWorkload <- newWorksheet(handler, index, j)
		wasWorthStarting = true
	}

	return
}

func (j *jobsheet) Add(handlers []Handler) {
	var addedWorkCount int

	for i, l := 0, j.workCount; i < l; i++ {
		if j.addOne(handlers[i], i) {
			addedWorkCount++
		} else {
			break
		}
	}

	j.addedWorkCount <- addedWorkCount
}

func (j *jobsheet) AddRepeated(handler Handler, count int) {
	var addedWorkCount int

	for i, l := 0, j.workCount; i < l; i++ {
		if j.addOne(handler, i) {
			addedWorkCount++
		} else {
			break
		}
	}

	j.addedWorkCount <- addedWorkCount
}

func (j *jobsheet) isWorthStarting(index int) bool {
	defer j.resultMutex.RUnlock()
	j.resultMutex.RLock()

	return j.result == nil || j.resultIndex > index
}

func (j *jobsheet) SetResult(result error, resultIndex int) {
	defer j.resultMutex.Unlock()
	j.resultMutex.Lock()

	if i := j.resultIndex; resultIndex < i || i == 0 {
		j.result, j.resultIndex = result, resultIndex
	}
}

// runs in own goroutine
func (j *jobsheet) startWork() {
	for {
		select {
		case worksheet := <-j.pendingWorkload:
			worksheet.Work()
		case <-j.stopWork:
			return
		}
	}
}

func (j *jobsheet) Work() {
	for i := 0; i < numCPU; i++ {
		go j.startWork()
	}
}

func (j *jobsheet) WaitForSome(count int) {
	for i := 0; i < count; i++ {
		<-j.completedWorkload
	}
}

func (j *jobsheet) stop() {
	for i := 0; i < numCPU; i++ {
		j.stopWork <- struct{}{}
	}
}

func (j *jobsheet) Wait() {
	var workCount int

outerLoop:
	for {
		select {
		case <-j.completedWorkload:
			workCount--
		case wc := <-j.addedWorkCount:
			workCount += wc
			break outerLoop
		}
	}

	if workSubcount := workCount / numCPU; workSubcount > 0 {
		var wg sync.WaitGroup

		wg.Add(numCPU)

		for i := 0; i < numCPU; i++ {
			go func() {
				defer wg.Done()

				j.WaitForSome(workSubcount)
			}()
		}

		wg.Wait()

		workCount -= workSubcount * numCPU
	}

	if workCount > 0 {
		j.WaitForSome(workCount)
	}

	go j.stop()
}
