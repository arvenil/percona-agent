package qh

import (
	"os"
	"time"
//	"fmt"
	"github.com/percona/percona-cloud-tools/agent"
	agentLog "github.com/percona/percona-cloud-tools/agent/log"
	mysqlLog "github.com/percona/percona-go-mysql/log"
	"github.com/percona/percona-go-mysql/log/parser"
)

type Worker struct {
	cc *agent.ControlChannels
	job *Job
	resultChan chan *Result
	doneChan chan *Worker
	// --
	log *agentLog.LogWriter
}

type Job struct {
	SlowLogFile string
	Runtime time.Duration
	StartOffset int64
	StopOffset int64
	ExampleQueries bool
}

type Result struct {
	Error error `json:",omitempty"`
	Global *mysqlLog.GlobalClass `json:",omitempty"`
	Classes []*mysqlLog.QueryClass `json:",omitempty"`
}

func NewWorker(cc *agent.ControlChannels, job *Job, resultChan chan *Result, doneChan chan *Worker) *Worker {
	w := &Worker{
		cc: cc,
		job: job,
		resultChan: resultChan,
		doneChan: doneChan,
		log: agentLog.NewLogWriter(cc.LogChan, "qh-worker"),
	}
	return w
}

func (w *Worker) Run() {
	// Whenever and however we return, send qh-manager our result and
	// tell it we're done so it frees our spot for another concurrent
	// worker.
	result := new(Result)
	defer func() {
		w.resultChan <-result
		w.doneChan <-w
	}()

	// Open the slow log file.
	file, err := os.Open(w.job.SlowLogFile)
	if err != nil {
		result.Error = err
		return
	}

	// Seek to the start offset, if any.
	// @todo error if start off > file size
	if w.job.StartOffset != 0 {
		// @todo handle error
		file.Seek(int64(w.job.StartOffset), os.SEEK_SET)
	}

	// Create a slow log parser and run it.  It sends events log events
	// via its channel.
	p := parser.NewSlowLogParser(file, false) // false=debug off
	if err != nil {
		result.Error = err
		return
	}
	go p.Run()

	// The global class has info and stats for all events.
	// Each query has its own class, defined by the checksum of its fingerprint.
	global := mysqlLog.NewGlobalClass()
	queries := make(map[string]*mysqlLog.QueryClass)
	for event := range p.EventChan {
		if int64(event.Offset) > w.job.StopOffset {
			break
		}

		// Add the event to the global class.
		global.AddEvent(event)

		// Get the query class to which the event belongs.
		fingerprint := mysqlLog.Fingerprint(event.Query)
		classId := mysqlLog.Checksum(fingerprint)
		class, haveClass := queries[classId]
		if !haveClass {
			class = mysqlLog.NewQueryClass(classId, fingerprint)
			queries[classId] = class
		}

		// Add the event to its query class.
		class.AddEvent(event)
	}

	// Done parsing the slow log.  Finalize the global and query classes (calculate
	// averages, etc.).
	for _, class := range queries {
		class.Finalize()
	}
	global.Finalize(uint64(len(queries)))

	nQueries := len(queries)
	classes := make([]*mysqlLog.QueryClass, nQueries)
	for _, class := range queries {
		// Decr before use; can't classes[--nQueries] in Go.
		nQueries--
		classes[nQueries] = class
	}

	// Save the result.  It will be sent by defer when we return.
	result.Error = nil
	result.Global = global
	result.Classes = classes

	return
}
