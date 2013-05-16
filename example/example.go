// example.go
package main

import (
	"code.google.com/p/go.net/websocket"
	"flag"
	"fmt"
	"net/http"
	"github.com/kvij/wsevents"
)

// Listen on port 80 by default
var port *int = flag.Int("p", 80, "Port to listen.")

// Instanciate two EventManagers
var EventManager1 *wsevents.EventManager = wsevents.NewEventManager()
var EventManager2 *wsevents.EventManager = wsevents.NewEventManager()

// Implements interface Dispatcher
type broadcast string

// Decide to which connection we have to send our package, in this case all.
func (bc *broadcast) Match(id int) bool {
	return true
}

// Make the package that is send to "id"
func (bc *broadcast) BuildPackage(id int) interface{} {
	return &wsevents.EventPackage{
		id,
		"message",
		bc,
	}
}

// Handle errors
func (bc *broadcast) Error(err error, pack interface{}) {}

func handler(c chan wsevents.EventPackage, thisEm, other *wsevents.EventManager) {
	for {
		pack, ok := <-c

		// Channel was closed, should only happen when EventManager.Unregister(c) is called.
		if !ok {
			return
		}

		/*
		  The following swtich handles all incomming events.
		  FIXME: The "default:" event is only going to work with one eventhandler.

		  Builtin events:
		  * When a new websocket is registerd a CONNECTED event is fired.
		  * When a websocket is closed a DISCONECTED event is fired with the error in EventData.
		  * When a websocket has been transfered a TANSFERRED event is fired. EventData contains a map with the new id (newId)
		  and a pointer to the new EventManager (dest).
		*/
		switch pack.Event {
		// Broadcast the received string unsing EventManager.Dispatch(d Dispatcher)
		case "message":
			if msg, ok := pack.EventData.(string); ok {
				bc := broadcast(msg)
				thisEm.Dispatch(&bc)
			}
		// Transfer to the other EventManager using EventManager.Transfer(id int, destination *EventManager)
		case "other":
			fmt.Println("Changing: ", pack.Id)
			thisEm.Transfer(pack.Id, other)
		// Send the received event only to its sender with EventManager.Send(id int, interface{})
		case "echo":
			if msg, ok := pack.EventData.(string); ok {
				thisEm.Send(pack.Id, msg)
			}
		// Unregister this handler. As it is the only handler for this EventManager its rendered useless.
		case "stop":
			thisEm.Unregister(c)
		// Handle the builtin events
		case "DISCONECTED", "CONNECTED", "TRANSFERRED":
		// Assume that if none of the above matches we don't know what to do
		default:
			thisEm.Send(pack.Id, "Unknown event")
		}
	}
}

func main() {
	// Get commandline options
	flag.Parse()

	// Register the handlers, EventManager.Register() returns a new chan wsevents.EventPackage
	go handler(EventManager1.Register(), EventManager1, EventManager2)
	go handler(EventManager2.Register(), EventManager2, EventManager1)

	fmt.Printf("http://127.0.0.1:%d/\n\n", *port)

	// Listen on EventManager1 for new connections
	http.Handle("/ws", websocket.Handler(func(ws *websocket.Conn) { EventManager1.Listen(ws) }))
	err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil)

	if err != nil {
		panic("ListenAndServe: " + err.Error())
	}
}
