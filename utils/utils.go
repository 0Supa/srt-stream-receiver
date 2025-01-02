package utils

import (
	"bufio"
	"log"
	"net"
	"os"
	"sync"

	"github.com/haivision/srtgo"
)

type Listener struct {
	Index     int
	Socket    *srtgo.SrtSocket
	Address   *net.UDPAddr
	Active    bool
	WaitGroup *sync.WaitGroup
	Streamid  string
}

type Publisher struct {
	Stats chan *PublisherEvent
}

type PublisherEvent struct {
	Streamid string
	Type     string
	Data     *srtgo.SrtStats
	Conn     PublisherConn
}

type PublisherConn struct {
	Latency int
}

var AllowedStreamIDs = make(map[string]bool)

var StatsChannel = make(chan *PublisherEvent)

func init() {
	readFile, err := os.Open("allowlist.txt")

	if err != nil {
		log.Panicln("Failed opening allowlist file", err)
	}
	fileScanner := bufio.NewScanner(readFile)

	fileScanner.Split(bufio.ScanLines)

	for fileScanner.Scan() {
		AllowedStreamIDs[fileScanner.Text()] = true
	}

	readFile.Close()
}
