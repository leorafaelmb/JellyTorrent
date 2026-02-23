package tracker

// UDPProtocolID is the "magic constant" used in connect requests to trackers using the UDP tracker
// protocol
const UDPProtocolID = 0x41727101980

// UDP tracker request action field constants
const (
	ActionConnect = iota
	ActionAnnounce
	ActionScrape
	ActionError
)

// tracker request event field constants
const (
	EventNone = iota
	EventCompleted
	EventStarted
	EventStopped
)

// EventStrings maps event constants to their HTTP announce parameter values
var EventStrings = map[uint32]string{
	EventCompleted: "completed",
	EventStarted:   "started",
	EventStopped:   "stopped",
}
