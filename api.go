package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/0supa/srt-stream-receiver/meta"
	"github.com/pion/webrtc/v4"
	"golang.org/x/net/websocket"
)

type statsPayload struct {
	Streamid string           `json:"streamid"`
	Type     string           `json:"type"`
	Data     statsPayloadData `json:"data"`
}

type statsPayloadData struct {
	RTT     float64 `json:"rtt"`
	Bitrate int     `json:"bitrate"`
	Latency int     `json:"latency"`
}

type baseCommand struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func initAPI(httpAddr string) {
	go func() {
		for event := range meta.StatsChannel {
			res := statsPayload{
				Streamid: event.Streamid,
				Type:     event.Type,
			}

			if event.Data != nil {
				res.Data = statsPayloadData{
					RTT:     event.Data.MsRTT,
					Bitrate: int(event.Data.MbpsRecvRate * 1000),
					Latency: event.Conn.Latency,
				}
			}

			message, err := json.Marshal(res)
			if err != nil {
				log.Println("JSON marshal failed", err)
				continue
			}

			meta.ListenersRW.Lock()
			listeners := meta.ListenersMap[event.Streamid]
			meta.ListenersRW.Unlock()
			for _, listener := range listeners {
				c := listener.Connection.WebRTC.DataChannels.Stats
				if c != nil {
					err := c.Send(message)
					log.Println("MESSAGE SENT")
					if err != nil {
						if err == io.ErrClosedPipe {
							// meta.ListenersRW.Lock()
							// delete(listeners, id)
							// meta.ListenersRW.Unlock()
							// log.Println("channel killed")
							continue
						}

						log.Println("Channel send failed", err)
					}
				}
			}
		}
	}()

	http.Handle("/", http.FileServer(http.Dir("./web")))
	http.Handle("/webrtc/websocket", websocket.Handler(websocketServer))

	log.Println("HTTP listening on " + httpAddr)
	log.Fatal(http.ListenAndServe(httpAddr, nil))
}

func websocketServer(ws *websocket.Conn) {
	// Create a new RTCPeerConnection
	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{
					"stun:stun.l.google.com:19302",
					"stun:stun.cloudflare.com:3478",
				},
			},
		},
	})
	if err != nil {
		panic(err)
	}

	// When Pion gathers a new ICE Candidate send it to the client. This is how
	// ice trickle is implemented. Everytime we have a new candidate available we send
	// it as soon as it is ready. We don't wait to emit a Offer/Answer until they are
	// all available
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}

		cJSON, err := json.Marshal(c.ToJSON())
		if err != nil {
			log.Println(err)
			return
		}

		data := baseCommand{
			Type: "WEBRTC_CANDIDATE",
			Data: cJSON,
		}

		outbound, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			panic(marshalErr)
		}

		if _, err = ws.Write(outbound); err != nil {
			panic(err)
		}
	})

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("ICE Connection State has changed: %s\n", connectionState.String())

		if connectionState == webrtc.ICEConnectionStateFailed {
			if closeErr := peerConnection.Close(); closeErr != nil {
				panic(closeErr)
			}
		}
	})

	l := &meta.Listener{
		Index: int(time.Now().UnixMicro()),
		Connection: meta.Connection{
			WebRTC: &meta.WebRTCConn{
				Peer:    peerConnection,
				Senders: []*webrtc.RTPSender{},
			},
		},
		Active: true,
	}

	var buf []byte
	for {
		err := websocket.Message.Receive(ws, &buf)
		if err != nil {
			break
		}

		handleWSMessage(ws, l, buf)
	}
}

func handleWSMessage(ws *websocket.Conn, l *meta.Listener, input []byte) {
	var cmd baseCommand
	json.Unmarshal(input, &cmd)
	// fmt.Printf("%s\n", input)

	peerConnection := l.Connection.WebRTC.Peer

	switch cmd.Type {
	case "LISTEN":
		{
			var data struct {
				Streamid string `json:"streamid"`
			}
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				log.Println(err)
				return
			}

			streamid := data.Streamid

			if _, found := meta.AllowedStreamIDs[streamid]; !found {
				return
			}

			l.Streamid = streamid

			meta.PublishersRW.Lock()
			publisher := meta.PublishersMap[streamid]
			meta.PublishersRW.Unlock()

			if publisher != nil {
				for _, track := range publisher.Tracks {
					log.Println("ADDING track")
					rtpSender, err := peerConnection.AddTrack(track)
					if err != nil {
						log.Println(streamid, "failed adding track", err)
						return
					}
					// webrtcutil.HandleSender(rtpSender)

					l.Connection.WebRTC.Senders = append(l.Connection.WebRTC.Senders, rtpSender)
				}
			}

			meta.UpdateListener(l)
		}

	case "WEBRTC_OFFER":
		{
			var offer webrtc.SessionDescription
			if err := json.Unmarshal(cmd.Data, &offer); err != nil {
				log.Println(err)
				return
			}

			if offer.SDP != "" {
				if err := peerConnection.SetRemoteDescription(offer); err != nil {
					panic(err)
				}

				answer, answerErr := peerConnection.CreateAnswer(nil)
				if answerErr != nil {
					panic(answerErr)
				}

				if err := peerConnection.SetLocalDescription(answer); err != nil {
					panic(err)
				}

				answerJSON, err := json.Marshal(answer)
				if err != nil {
					log.Println(err)
					return
				}

				data := baseCommand{
					Type: "WEBRTC_ANSWER",
					Data: answerJSON,
				}

				outbound, marshalErr := json.Marshal(data)
				if marshalErr != nil {
					panic(marshalErr)
				}

				if _, err := ws.Write(outbound); err != nil {
					panic(err)
				}
			}
		}

	case "WEBRTC_CANDIDATE":
		{
			var candidate webrtc.ICECandidateInit
			if err := json.Unmarshal(cmd.Data, &candidate); err != nil {
				log.Println(err)
				return
			}

			if candidate.Candidate != "" {
				if err := peerConnection.AddICECandidate(candidate); err != nil {
					panic(err)
				}
			}
		}

	default:
		log.Println("unknown message", cmd.Type)
	}
}
