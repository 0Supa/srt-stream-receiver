package main

// #cgo LDFLAGS: -lsrt
// #include <srt/srt.h>
import "C"

import (
	"io"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/0supa/srt-stream-receiver/meta"
	"github.com/haivision/srtgo"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/h264reader"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
)

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
		err := publish(sock, addr, streamid)
		if err != nil {
			log.Println(streamid, "publish error", err)
		}
	} else {
		listen(sock, addr, streamid)
	}
}

func listen(sock *srtgo.SrtSocket, addr *net.UDPAddr, streamid string) {
	var wg sync.WaitGroup

	log.Printf("%s - listen: %s\n", addr, streamid)

	l := &meta.Listener{
		Index: int(time.Now().UnixMicro()),
		Connection: meta.Connection{
			SRT: &meta.SrtConn{
				Socket:  sock,
				Address: addr,
			},
		},
		Active:    true,
		WaitGroup: &wg,
		Streamid:  streamid,
	}

	l.WaitGroup.Add(1)

	meta.UpdateListener(l)

	l.WaitGroup.Wait()

	l.Connection.SRT.Socket.Close()
	meta.ListenersRW.Lock()
	delete(meta.ListenersMap[streamid], l.Index)
	meta.ListenersRW.Unlock()
}

func publish(sock *srtgo.SrtSocket, addr *net.UDPAddr, streamid string) (err error) {
	log.Printf("%s - publish: %s\n", addr, streamid)

	srtLatency, err := sock.GetSockOptInt(C.SRTO_LATENCY)
	if err != nil {
		return
	}

	meta.PublishersRW.Lock()
	publisher := meta.PublishersMap[streamid]
	meta.PublishersRW.Unlock()

	videoTrack := publisher.Tracks[0]
	audioTrack := publisher.Tracks[1]

	srtReader := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "quiet",
		"-i", "-",
		"-map", "0:v:0",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-c:v", "copy",
		"-f", "rawvideo", "pipe:1",
		"-map", "0:a:0",
		"-c:a", "libopus", "-ac", "2",
		"-f", "opus", "pipe:2")

	pipeReader, pipeWriter := io.Pipe()
	srtReader.Stdin = pipeReader

	videoPipe, err := srtReader.StdoutPipe()
	if err != nil {
		return
	}
	audioPipe, err := srtReader.StderrPipe()
	if err != nil {
		return
	}

	// var stderr bytes.Buffer
	// srtReader.Stderr = &stderr

	err = srtReader.Start()
	if err != nil {
		return
	}

	go func() {
		reader, oggHeader, err := oggreader.NewWith(audioPipe)
		if err != nil {
			log.Println("oggreader error:", err)
			return
		}

		var lastHeader *oggreader.OggPageHeader
		for {
			data, header, err := reader.ParseNextPage()
			if err != nil {
				log.Println("oggreader error:", err)
				return
			}

			samples := header.GranulePosition
			if lastHeader != nil {
				samples -= lastHeader.GranulePosition
			}
			log.Println(samples, oggHeader.SampleRate)

			lastHeader = header

			err = audioTrack.WriteSample(media.Sample{
				Data:     data,
				Duration: time.Duration((samples/48000)*1000) * time.Millisecond,
			})
			if err != nil {
				log.Println("sample error:", err)
			}
		}
	}()

	go func() {
		reader, err := h264reader.NewReader(videoPipe)
		if err != nil {
			log.Println("h264reader error:", err)
			return
		}

		sps := []byte{}
		pps := []byte{}

		for {
			nal, err := reader.NextNAL()
			if err != nil {
				log.Println("h264reader error:", err)
				// log.Println(stderr.String())
				return
			}

			// convert to annex b
			nal.Data = append([]byte{0x00, 0x00, 0x00, 0x01}, nal.Data...)

			// save sps for later use
			if nal.UnitType == 7 {
				sps = nal.Data
			} else if nal.UnitType == 8 { // save pps
				pps = nal.Data
			} else if nal.UnitType == 5 { // make stream header
				log.Println("i-frame")

				header := append(
					sps,
					pps...,
				)

				nal.Data = append(
					header,
					nal.Data...,
				)
			}

			err = videoTrack.WriteSample(media.Sample{
				Data: nal.Data[:],
				// Duration: time.Second / 60,
			})
			if err != nil {
				log.Println("sample error:", err)
			}
		}
	}()

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

		packet := buf[:n]

		_, err = pipeWriter.Write(packet)
		if err != nil {
			log.Println("Error writing to FFmpeg stdin:", err)
			break
		}

		meta.ListenersRW.Lock()
		listeners := meta.ListenersMap[streamid]
		meta.ListenersRW.Unlock()
		for _, listener := range listeners {
			if listener.Connection.SRT == nil {
				continue
			}

			_, err = listener.Connection.SRT.Socket.Write(packet)
			if err != nil && listener.Active {
				listener.Active = false
				listener.WaitGroup.Done()

				log.Println(listener.Streamid, err)
			}
		}
	}

	pipeWriter.Close()
	videoPipe.Close()
	srtReader.Wait()
	updateTicker.Stop()
	meta.StatsChannel <- &meta.PublisherEvent{Streamid: streamid, Type: "stop"}

	return
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
