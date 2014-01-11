package mock

import (
	"time"
)

type Ticker struct {
	syncChan chan bool
	tickerChan chan time.Time
}

func NewTicker(syncChan chan bool, tickerChan chan time.Time) *Ticker {
	t := &Ticker{
		syncChan: syncChan,
		tickerChan: tickerChan,
	}
	return t
}

func (t *Ticker) Sync(now int64) {
	if t.syncChan != nil {
		<-t.syncChan
	}
	return
}

func (t *Ticker) TickerChan() <-chan time.Time {
	return t.tickerChan
}

func (t *Ticker) Stop() {
	return
}