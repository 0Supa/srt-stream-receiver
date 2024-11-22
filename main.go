package main

// #cgo LDFLAGS: -lsrt
// #include <srt/srt.h>
import "C"

import (
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/0supa/srt-stream-receiver/meta"
	"github.com/0supa/srt-stream-receiver/webrtc"
	"github.com/haivision/srtgo"
)

type listener struct {
	Index     int
	Socket    *srtgo.SrtSocket
	Address   *net.UDPAddr
	Active    bool
	WaitGroup *sync.WaitGroup
	Streamid  string
}

type publisher struct {
	Stats chan *publisherEvent
}

type publisherEvent struct {
	Streamid string
	Type     string
	Data     *srtgo.SrtStats
	Conn     publisherConn
}

type publisherConn struct {
	Latency int
}

// store listeners in a nested map with the streamid as the key
var listenersMap = make(map[string]map[int]*listener)
var listenersLock sync.RWMutex

func listenCallback(sock *srtgo.SrtSocket, version int, addr *net.UDPAddr, streamid string) bool {
	log.Printf("SRT socket connecting - hsVersion: %d, streamid: %s\n", version, streamid)

	// socket not in allowed ids -> reject
	if _, found := meta.AllowedStreamIDs[strings.TrimPrefix(streamid, "publish:")]; !found {
		log.Println("Rejected connection - streamid:", streamid)
		// set custom reject reason
		sock.SetRejectReason(srtgo.RejectionReasonUnauthorized)
		return false
	}

	// allow connection
	return true
}

func handler(sock *srtgo.SrtSocket, addr *net.UDPAddr) {
	defer sock.Close()

	streamid, err := sock.GetSockOptString(C.SRTO_STREAMID)
	if err != nil {
		log.Println(err)
		return
	}

	if strings.HasPrefix(streamid, "publish:") {
		streamid = strings.TrimPrefix(streamid, "publish:")
		publish(sock, addr, streamid)
	} else {
		listen(sock, addr, streamid)
	}
}

func listen(sock *srtgo.SrtSocket, addr *net.UDPAddr, streamid string) {
	var wg sync.WaitGroup

	log.Printf("%s - listen: %s\n", addr, streamid)

	l := &listener{
		Index:     int(time.Now().UnixMicro()),
		Socket:    sock,
		Address:   addr,
		Active:    true,
		WaitGroup: &wg,
		Streamid:  streamid,
	}

	l.WaitGroup.Add(1)

	listenersLock.Lock()
	if listenersMap[streamid] == nil {
		listenersMap[streamid] = make(map[int]*listener)
	}
	listenersMap[streamid][l.Index] = l
	listenersLock.Unlock()

	l.WaitGroup.Wait()

	l.Socket.Close()
	listenersLock.Lock()
	delete(listenersMap[streamid], l.Index)
	listenersLock.Unlock()
}

func publish(sock *srtgo.SrtSocket, addr *net.UDPAddr, streamid string) {
	log.Printf("%s - publish: %s\n", addr, streamid)

	srtLatency, err := sock.GetSockOptInt(C.SRTO_LATENCY)
	if err != nil {
		log.Println(streamid, err)
		return
	}

	meta.StatsChannel <- &meta.PublisherEvent{Streamid: streamid, Type: "start"}

	updateTicker := time.NewTicker(500 * time.Millisecond)
	go func() {
		for range updateTicker.C {
			stats, err := sock.Stats()
			if err != nil {
				log.Println(streamid, err)
			}

			meta.StatsChannel <- &meta.PublisherEvent{
				Streamid: streamid,
				Type:     "update",
				Data:     stats,
				Conn: meta.PublisherConn{
					Latency: srtLatency,
				},
			}
		}
	}()

	webrtc.Startup()

	buf := make([]byte, 1316) // TS_UDP_LEN
	for {
		n, err := sock.Read(buf)
		if err != nil {
			log.Println(streamid, err)
			break
		}

		// handle EOF
		if n == 0 {
			break
		}

		vtrack := webrtc.VideoTrack
		if vtrack != nil {
			if _, err := webrtc.VideoTrack.Write(buf[:n]); err != nil {
				if errors.Is(err, io.ErrClosedPipe) {
					// The peerConnection has been closed.
					return
				}

				panic(err)
			}
		}

		listeners := listenersMap[streamid]
		for _, listener := range listeners {
			_, err = listener.Socket.Write(buf[:n])
			if err != nil && listener.Active {
				listener.Active = false
				listener.WaitGroup.Done()

				log.Println(listener.Streamid, err)
			}
		}
	}

	updateTicker.Stop()
	meta.StatsChannel <- &meta.PublisherEvent{Streamid: streamid, Type: "stop"}
}

func main() {
	var wg sync.WaitGroup

	var socketPort uint16 = 8080
	var httpAddr string = ":8181"

	options := make(map[string]string)
	options["blocking"] = "0"
	options["transtype"] = "live"
	options["latency"] = "300"

	sck := srtgo.NewSrtSocket("0.0.0.0", socketPort, options)

	sck.SetListenCallback(listenCallback)

	err := sck.Listen(5)
	if err != nil {
		log.Fatalf("Listen failed: %v \n", err.Error())
	}
	log.Println("SRT Socket listening on port", socketPort)

	go initAPI(httpAddr)

	wg.Add(1)

	go func() {
		defer wg.Done()
		for {
			sock, addr, err := sck.Accept()
			if err != nil {
				log.Println("Accept failed", err)
			}
			go handler(sock, addr)
		}
	}()

	wg.Wait()
}
