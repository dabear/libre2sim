package libre2

import (
	"fmt"
	"sync"
	"time"
)

// Event categories (Event.Cat), which the control panel filters on.
const (
	CatNFC    = "nfc"
	CatBLE    = "ble"
	CatSensor = "sensor"
)

// DefaultEventCap is how many events a Sensor keeps.
const DefaultEventCap = 200

// Event is one thing that happened to the sensor: an NFC call, a BLE link
// event, a packet, a fault, a change of glucose mode.
type Event struct {
	Seq  uint64    `json:"seq"` // 1, 2, 3 …, never reused while libre2sim runs
	Time time.Time `json:"time"`
	Cat  string    `json:"cat"`
	Kind string    `json:"kind"`
	Msg  string    `json:"msg"`
}

// EventLog is a fixed-size ring of the latest events. It is safe for
// concurrent use and kept in memory only: a restart (POST /new) empties it.
type EventLog struct {
	mu   sync.Mutex
	buf  []Event
	next int    // where the next event goes
	n    int    // how many slots are filled
	seq  uint64 // sequence number of the latest event
	now  func() time.Time
}

// NewEventLog keeps the latest capacity events (at least 1).
func NewEventLog(capacity int) *EventLog {
	if capacity < 1 {
		capacity = 1
	}
	return &EventLog{buf: make([]Event, capacity), now: time.Now}
}

// Add records an event and returns its sequence number.
func (l *EventLog) Add(cat, kind, format string, args ...interface{}) uint64 {
	msg := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	l.buf[l.next] = Event{Seq: l.seq, Time: l.now(), Cat: cat, Kind: kind, Msg: msg}
	l.next = (l.next + 1) % len(l.buf)
	if l.n < len(l.buf) {
		l.n++
	}
	return l.seq
}

// Since returns the events with a sequence number above since, oldest first,
// and the latest sequence number. Events that fell out of the ring are gone.
func (l *EventLog) Since(since uint64) (events []Event, last uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	events = []Event{}
	for i := 0; i < l.n; i++ {
		e := l.buf[(l.next-l.n+i+len(l.buf))%len(l.buf)]
		if e.Seq > since {
			events = append(events, e)
		}
	}
	return events, l.seq
}
