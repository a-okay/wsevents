// Copyright 2013 The Karel van IJperen. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"code.google.com/p/go.net/websocket"
	"flag"
	"fmt"
	"github.com/kvij/wsevents"
	"net/http"
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
	return &map[string]interface{}{
		"Id":        id,
		"Event":     "message",
		"EventData": bc,
	}
}

// Handle errors...
func (bc *broadcast) Error(err error, pack interface{}) {}

func handler(c chan *wsevents.EventPackage, other *wsevents.EventManager) {
	for {
		handled := true
		pack, ok := <-c

		// Channel was closed, should only happen when EventManager.Unregister(c) is called.
		if !ok {
			return
		}

		/*
		  The following swtich handles all incomming events.

		  Builtin events:
		  * When a new websocket is registerd a CONNECTED event is fired.
		  * When a websocket is closed a DISCONECTED event is fired with the error in EventData.
		  * When a websocket has been transfered a TANSFERRED event is fired. EventData contains a map with the new id (newId)
		    and a pointer to the new EventManager (dest).
		  * When all registered handlers(wsevents.RegisterHandler) do EventPackage.Handled(false) the event is seen as unhandled
		    and the DEFAULT event is fired. EventData contains a pointer to the original EventPackage.
		*/
		switch pack.Event {
		// Broadcast the received string unsing EventManager.Dispatch(d Dispatcher)
		case "message":
			if msg, ok := pack.EventData.(string); ok {
				bc := broadcast(msg)
				pack.EventManager.Dispatch(&bc)
			}
		// Transfer to the other EventManager using EventManager.Transfer(id int, destination *EventManager)
		case "other":
			fmt.Println("Changing: ", pack.Id)
			pack.EventManager.Transfer(pack.Id, other)
			fmt.Println("Changed")
		// Send the received event only to its sender with EventManager.Send(id int, interface{})
		case "echo":
			if msg, ok := pack.EventData.(string); ok {
				pack.EventManager.Send(pack.Id, msg)
			}
		// Unregister this handler. As it is the only handler for this EventManager its rendered useless.
		case "stop":
			pack.EventManager.Unregister(c)
		// Handle the builtin events
		case "DISCONECTED", "CONNECTED", "TRANSFERRED":
		case "DEFAULT":
			pack.EventManager.Send(pack.Id, "Unknown event")
		default:
			handled = false
		}

		pack.Handled(handled)
	}
}

func main() {
	// Get commandline options
	flag.Parse()

	// Register the handlers, EventManager.Register() returns a new chan wsevents.EventPackage
	go handler(EventManager1.RegisterHandler(), EventManager2)
	go handler(EventManager2.RegisterHandler(), EventManager1)

	fmt.Printf("http://127.0.0.1:%d/\n\n", *port)

	// Listen on EventManager1 for new connections
	http.Handle("/ws", websocket.Handler(func(ws *websocket.Conn) { EventManager1.Listen(ws) }))
	err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil)

	if err != nil {
		panic("ListenAndServe: " + err.Error())
	}
}
