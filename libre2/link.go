package libre2

// Char names the sensor's characteristics a session talks on. A Libre 2
// exposes one service (FDE3) with a write characteristic (F001) for the
// streaming unlock and a notify characteristic (F002) for the 46-byte
// glucose packets.
type Char int

const (
	CharNone Char = iota
	CharWrite
	CharNotify
)

func (c Char) String() string {
	return [...]string{"none", "write(F001)", "notify(F002)"}[c]
}

// Link is one central's connection, as the transport provides it.
type Link interface {
	Addr() string
	// Notify sends a notification; it fails if the central has not subscribed.
	Notify(c Char, b []byte) error
	// Respond answers the central's read of c.
	Respond(c Char, b []byte)
	Close()
}

// Handler is the sensor's side of one connection. The transport calls it
// from that connection's own goroutine, in the order the central's requests
// arrived, so an implementation needs no locking of its own.
type Handler interface {
	Write(c Char, data []byte)
	Subscribed(c Char, enabled bool)
	Read(c Char)
	Closed()
}
