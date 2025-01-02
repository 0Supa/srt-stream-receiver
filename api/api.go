package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/0supa/srt-stream-receiver/utils"
	"github.com/gorilla/websocket"
)

type baseCommand struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

type connContext struct {
	Index    int
	Conn     *websocket.Conn
	Streamid string
}

var activeConnections = make(map[string]map[int]*connContext)
var connectionsLock sync.RWMutex

func readMessage(ctx *connContext, input []byte) {
	var cmd baseCommand
	json.Unmarshal(input, &cmd)

	switch cmd.Type {
	case "LISTEN":
		{
			streamid, ok := cmd.Data["streamid"].(string)
			if !ok {
				return
			}

			if _, found := utils.AllowedStreamIDs[streamid]; !found || ctx.Streamid == streamid {
				return
			}

			connectionsLock.Lock()
			if ctx.Streamid != "" {
				delete(activeConnections[ctx.Streamid], ctx.Index)
			}

			ctx.Streamid = streamid
			if activeConnections[streamid] == nil {
				activeConnections[streamid] = make(map[int]*connContext)
			}
			activeConnections[streamid][ctx.Index] = ctx
			connectionsLock.Unlock()
		}
	}
}

func handleConnection(conn *websocket.Conn) {
	ctx := &connContext{
		Index:    int(time.Now().UnixMicro()),
		Conn:     conn,
		Streamid: "",
	}

	defer func() {
		conn.Close()
		if ctx.Streamid != "" {
			connectionsLock.Lock()
			delete(activeConnections[ctx.Streamid], ctx.Index)
			connectionsLock.Unlock()
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("Websocket read failed", err)
			break
		}

		readMessage(ctx, message)
	}
}

func InitAPI(httpAddr string) {
	go func() {
		for event := range utils.StatsChannel {
			res := map[string]interface{}{
				"streamid": event.Streamid,
				"type":     event.Type,
				"data":     nil,
			}

			if event.Data != nil {
				res["data"] = map[string]interface{}{
					"rtt":     event.Data.MsRTT,
					"bitrate": int(event.Data.MbpsRecvRate * 1000),
					"latency": event.Conn.Latency,
				}
			}

			// log.Printf("Event fired %+v\n", res)

			message, err := json.Marshal(res)
			if err != nil {
				log.Println("JSON marshal failed", err)
				continue
			}

			connectionsLock.RLock()
			listeners := activeConnections[event.Streamid]
			for _, listener := range listeners {
				err = listener.Conn.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					log.Println("Websocket write failed", err)
					continue
				}
			}
			connectionsLock.RUnlock()
		}
	}()

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		upgrader.CheckOrigin = func(r *http.Request) bool { return true }
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("Connection upgrade failed", err)
			return
		}

		handleConnection(conn)
	})

	log.Println("HTTP listening on " + httpAddr)
	log.Fatal(http.ListenAndServe(httpAddr, nil))
}
