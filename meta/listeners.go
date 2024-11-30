package meta

import (
	"net"
	"sync"

	"github.com/haivision/srtgo"
	"github.com/pion/webrtc/v4"
)

type SrtConn struct {
	Socket  *srtgo.SrtSocket
	Address *net.UDPAddr
}

type WebRTCConn struct {
	Senders      []*webrtc.RTPSender
	DataChannels struct {
		Stats *webrtc.DataChannel
	}
	Peer *webrtc.PeerConnection
}

type Connection struct {
	SRT    *SrtConn
	WebRTC *WebRTCConn
}

type Listener struct {
	Index     int
	Active    bool
	WaitGroup *sync.WaitGroup
	Streamid  string

	Connection Connection
}

// store listeners in a nested map with the streamid as the key
var ListenersMap = make(map[string]map[int]*Listener)
var ListenersRW sync.RWMutex
