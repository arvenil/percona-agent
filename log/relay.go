/*
    Copyright (c) 2014, Percona LLC and/or its affiliates. All rights reserved.

    This program is free software: you can redistribute it and/or modify
    it under the terms of the GNU Affero General Public License as published by
    the Free Software Foundation, either version 3 of the License, or
    (at your option) any later version.

    This program is distributed in the hope that it will be useful,
    but WITHOUT ANY WARRANTY; without even the implied warranty of
    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
    GNU Affero General Public License for more details.

    You should have received a copy of the GNU Affero General Public License
    along with this program.  If not, see <http://www.gnu.org/licenses/>
*/

package log

import (
	"fmt"
	"github.com/percona/cloud-protocol/proto"
	"github.com/percona/cloud-tools/pct"
	golog "log"
	"os"
	"time"
)

const (
	BUFFER_SIZE int = 10
)

type Relay struct {
	client        pct.WebsocketClient
	connected     bool
	logChan       chan *proto.LogEntry
	logLevel      byte
	logLevelChan  chan byte
	logger        *golog.Logger
	logFile       string
	logFileChan   chan string
	firstBuf      []*proto.LogEntry
	firstBufSize  int
	secondBuf     []*proto.LogEntry
	secondBufSize int
	lost          int
	statusChan    chan *Status
	offline       bool
}

type Status struct {
	LogLevel  byte
	LogFile   string
	Connected bool
	Channel   int
	Buffers   int
}

/**
 * client is optional.  If not given, only file logging is enabled if a log file
 * is sent to the LogFileChan().
 */
func NewRelay(client pct.WebsocketClient, logFile string, logLevel byte, offline bool) *Relay {
	r := &Relay{
		client:   client,
		logFile:  logFile,
		logLevel: logLevel,
		offline:  offline,
		// --
		logChan:      make(chan *proto.LogEntry, BUFFER_SIZE*3),
		logLevelChan: make(chan byte),
		logFileChan:  make(chan string),
		firstBuf:     make([]*proto.LogEntry, BUFFER_SIZE),
		secondBuf:    make([]*proto.LogEntry, BUFFER_SIZE),
		statusChan:   make(chan *Status, 1),
	}
	return r
}

func (r *Relay) LogChan() chan *proto.LogEntry {
	return r.logChan
}

func (r *Relay) LogLevelChan() chan byte {
	return r.logLevelChan
}

func (r *Relay) LogFileChan() chan string {
	return r.logFileChan
}

// @goroutine
func (r *Relay) Run() {
	r.setLogFile(r.logFile)

	// Connect if we were created with a client.  If this is slow, log entries
	// will be buffered and sent later.
	go r.connect()

	for {
		select {
		case entry := <-r.logChan:
			// Skip if log level too high, too verbose.
			if entry.Level > r.logLevel {
				continue
			}

			// Write to file if there's a file (usually there isn't).
			if r.logger != nil {
				r.logger.Printf("%s: %s: %s\n", entry.Service, proto.LogLevelName[entry.Level], entry.Msg)
			}

			// Send to API if we have a websocket client, and not in offline mode, and...
			if !r.offline && r.client != nil {
				// .. the websocket client is connected.
				if r.connected {
					r.send(entry, true) // buffer on err
				} else {
					// API not available right now, buffer and try later on reconnect.
					r.buffer(entry)
				}
			}
		case connected := <-r.client.ConnectChan():
			r.connected = connected
			r.internal(fmt.Sprintf("connected: %t", connected))
			if connected {
				// Connected for first time or reconnected.
				if len(r.firstBuf) > 0 {
					// Send log entries we saved while offline.
					r.resend()
				}
			} else {
				// waitErr() returned, so we got an error on websocket recv,
				// probably due to lost connection to API.  Keep trying to
				// reconnect in background, buffering log entries while offline.
				go r.connect()
			}
		case file := <-r.logFileChan:
			r.setLogFile(file)
		case level := <-r.logLevelChan:
			r.setLogLevel(level)
		} // select

		// If anyone is listening, send last internal status.
		status := &Status{
			LogLevel:  r.logLevel,
			LogFile:   r.logFile,
			Connected: r.connected,
			Channel:   len(r.logChan),
			Buffers:   r.firstBufSize + r.secondBufSize,
		}
		select {
		case r.statusChan <- status:
		default:
		}
	} // for
}

func (r *Relay) StatusChan() chan *Status {
	return r.statusChan
}

// Even the relayer needs to log stuff.
func (r *Relay) internal(msg string) {
	logEntry := &proto.LogEntry{
		Ts:      time.Now().UTC(),
		Service: "log",
		Level:   proto.LOG_WARNING,
		Msg:     msg,
	}
	r.logChan <- logEntry
}

// @goroutine
func (r *Relay) connect() {
	if r.client == nil || r.offline {
		// log file only
		return
	}
	r.client.Connect()
	go r.waitErr()
}

// @goroutine
func (r *Relay) waitErr() {
	// When a websocket closes, the err is returned on recv,
	// so we block on recv, not expecting any data, just
	// waiting for error/disconenct.
	var data interface{}
	if err := r.client.Recv(data, 0); err != nil {
		r.client.Disconnect()
	}
}

func (r *Relay) buffer(e *proto.LogEntry) {
	// First time we need to buffer delayed/lost log entries is closest to
	// the events that are causing problems, so we keep some, and when this
	// buffer is full...
	if r.firstBufSize < BUFFER_SIZE {
		r.firstBuf[r.firstBufSize] = e
		r.firstBufSize++
		return
	}

	// ...we switch to second, sliding window buffer, keeping the latest
	// log entries and a tally of how many we've had to drop from the start
	// (firstBuf) until now.
	if r.secondBufSize < BUFFER_SIZE {
		r.secondBuf[r.secondBufSize] = e
		r.secondBufSize++
		return
	}

	// secondBuf is full too.  This problem is long-lived.  Throw away the
	// buf and keep saving the latest log entries, counting how many we've lost.
	r.lost += r.secondBufSize
	for i := 0; i < BUFFER_SIZE; i++ {
		r.secondBuf[i] = nil
	}
	r.secondBuf[0] = e
	r.secondBufSize = 1
}

func (r *Relay) send(entry *proto.LogEntry, bufferOnErr bool) error {
	var err error
	r.client.Conn().SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err = r.client.Send(entry); err != nil {
		if bufferOnErr {
			r.buffer(entry)
		}
	}
	return err
}

func (r *Relay) resend() {
	for i := 0; i < BUFFER_SIZE; i++ {
		if r.firstBuf[i] != nil {
			if err := r.send(r.firstBuf[i], false); err == nil {
				// Remove from buffer on successful send.
				r.firstBuf[i] = nil
				r.firstBufSize--
			}
		}
	}

	if r.lost > 0 {
		logEntry := &proto.LogEntry{
			Ts:      time.Now().UTC(),
			Level:   proto.LOG_WARNING,
			Service: "log",
			Msg:     fmt.Sprintf("Lost %d log entries", r.lost),
		}
		// If the lost message warning fails to send, do not rebuffer it to avoid
		// the pathological case of filling the buffers with lost message warnings
		// caused by lost message warnings.
		r.send(logEntry, false)
		r.lost = 0
	}

	for i := 0; i < BUFFER_SIZE; i++ {
		if r.secondBuf[i] != nil {
			if err := r.send(r.secondBuf[i], false); err == nil {
				// Remove from buffer on successful send.
				r.secondBuf[i] = nil
				r.secondBufSize--
			}
		}
	}
}

func (r *Relay) setLogLevel(level byte) {
	if level < proto.LOG_EMERGENCY || level > proto.LOG_DEBUG {
		r.internal(fmt.Sprintf("Invalid log level: %d\n", level))
	} else {
		r.logLevel = level
	}
}

func (r *Relay) setLogFile(logFile string) {
	if logFile == "" {
		r.logger = nil
		r.logFile = ""
		return
	}

	var file *os.File
	if logFile == "STDOUT" {
		file = os.Stdout
	} else if logFile == "STDERR" {
		file = os.Stderr
	} else {
		var err error
		file, err = os.OpenFile(logFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0755)
		if err != nil {
			r.internal(err.Error())
			return
		}
	}
	logger := golog.New(file, "", golog.Ldate|golog.Ltime|golog.Lmicroseconds)
	r.logger = logger
	r.logFile = file.Name()
	r.logger.Println("logFile=" + r.logFile)
}
