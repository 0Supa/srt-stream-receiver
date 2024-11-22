package meta

import (
	"bufio"
	"log"
	"os"

	"github.com/haivision/srtgo"
)

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

var StatsChannel = make(chan *PublisherEvent)

var AllowedStreamIDs = make(map[string]bool)

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
