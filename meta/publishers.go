package meta

import (
	"bufio"
	"log"
	"os"
	"sync"

	"github.com/haivision/srtgo"
	"github.com/pion/webrtc/v4"
)

type Publisher struct {
	Tracks []*webrtc.TrackLocalStaticSample
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

var PublishersMap = make(map[string]*Publisher)
var PublishersRW sync.RWMutex

func init() {
	readFile, err := os.Open("allowlist.txt")

	if err != nil {
		log.Panicln("Failed opening allowlist file", err)
	}
	fileScanner := bufio.NewScanner(readFile)

	fileScanner.Split(bufio.ScanLines)

	for fileScanner.Scan() {
		streamid := fileScanner.Text() // one per line

		AllowedStreamIDs[streamid] = true

		PublishersRW.Lock()
		videoTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", streamid)
		if err != nil {
			panic(err)
		}
		audioTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio", streamid)
		if err != nil {
			panic(err)
		}
		PublishersMap[streamid] = &Publisher{
			Tracks: []*webrtc.TrackLocalStaticSample{videoTrack, audioTrack},
		}
		PublishersRW.Unlock()

	}

	readFile.Close()
}
