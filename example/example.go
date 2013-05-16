// example.go
package main

import (
	"code.google.com/p/go.net/websocket"
	"flag"
	"fmt"
	"net/http"
	"wsevents"
)

var port *int = flag.Int("p", 80, "Port to listen.")

var EventManager1 *wsevents.EventManager = wsevents.NewEventManager()
var EventManager2 *wsevents.EventManager = wsevents.NewEventManager()

type broadcast string

func (bc *broadcast) Match(id int) bool {
	return true
}

func (bc *broadcast) BuildPackage(id int) interface{} {
	return &wsevents.EventPackage{
		id,
		"message",
		bc,
	}
}

func (bc *broadcast) Error(err error, pack interface{}) {}

func handler(c chan wsevents.EventPackage, thisEm, other *wsevents.EventManager) {
	for {
		pack, ok := <-c

		if !ok {
			return
		}

		switch pack.Event {
		case "message":
			if msg, ok := pack.EventData.(string); ok {
				bc := broadcast(msg)
				thisEm.Dispatch(&bc)
			}
		case "other":
			fmt.Println("Changing: ", pack.Id)
			thisEm.Transfer(pack.Id, other)
		case "echo":
			if msg, ok := pack.EventData.(string); ok {
				thisEm.Send(pack.Id, msg)
			}
		case "stop":
			thisEm.Unregister(c)
		default:
			thisEm.Send(pack.Id, "Unknown event")
		}
	}
}

func main() {
	flag.Parse()

	go handler(EventManager1.Register(), EventManager1, EventManager2)
	go handler(EventManager2.Register(), EventManager2, EventManager1)

	fmt.Printf("http://127.0.0.1:%d/\n\n", *port)

	http.Handle("/ws", websocket.Handler(func(ws *websocket.Conn) { EventManager1.Listen(ws) }))
	err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil)

	if err != nil {
		panic("ListenAndServe: " + err.Error())
	}
}
