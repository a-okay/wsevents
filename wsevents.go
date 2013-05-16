// Copyright 2013 The Karel van IJperen. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package wsEvents supports an event model when using websockets.
package wsevents

import (
	"code.google.com/p/go.net/websocket"
	"errors"
	"strings"
	"sync"
)

var ErrConnNotFound = errors.New("Connection not found")

type Dispatcher interface {
	Match(id int) bool
	BuildPackage(id int) interface{}
	Error(err error, pack interface{})
}

type EventPackage struct {
	Id        int
	Event     string
	EventData interface{}
}

type EventManager struct {
	eventHandlers []chan EventPackage
	websockets    map[int]*websocket.Conn
	transfers     map[int]chan *EventPackage
	blockers      map[int]chan byte
	rLock         sync.WaitGroup
	wLock         sync.WaitGroup
}

// Constructor
func NewEventManager() *EventManager {
	return &EventManager{
		nil,
		make(map[int]*websocket.Conn),
		make(map[int]chan *EventPackage),
		make(map[int]chan byte),
		sync.WaitGroup{},
		sync.WaitGroup{},
	}
}

// Register eventhandler
func (em *EventManager) Register() (receiver chan EventPackage) {
	receiver = make(chan EventPackage, 50)
	em.eventHandlers = append(em.eventHandlers, receiver)

	return
}

// Remove eventhandler
func (em *EventManager) Unregister(receiver chan EventPackage) {
	em.rLock.Add(1)
	defer em.rLock.Done()
	em.wLock.Wait()

	for i, c := range em.eventHandlers {
		if receiver == c {
			close(c)
			em.eventHandlers[i], em.eventHandlers = em.eventHandlers[len(em.eventHandlers)-1], em.eventHandlers[:len(em.eventHandlers)-1]
			return
		}
	}
}

// Register and linsten on websocket
func (em *EventManager) Listen(ws *websocket.Conn) {
	id := em.addWebsocket(ws)
	em.listen(ws, id) // cal the actual listerer

	if blocker, ok := em.blockers[id]; ok {
		<-blocker
	}
}

// Send something to multiple connections
func (em *EventManager) Dispatch(d Dispatcher) {
	for id, ws := range em.websockets {
		if d.Match(id) {
			go func() {
				pack := d.BuildPackage(id)
				err := websocket.JSON.Send(ws, pack)

				if err != nil {
					d.Error(err, pack)
				}
			}()
		}
	}
}

// Send something to single connection
func (em *EventManager) Send(id int, pack interface{}) (err error) {
	err = ErrConnNotFound

	if ws, ok := em.websockets[id]; ok {
		err = websocket.JSON.Send(ws, pack)
	}

	return
}

// Transfer connection to dest EventManager
func (em *EventManager) Transfer(id int, dest *EventManager) {
	em.transfers[id] = make(chan *EventPackage)
	newId := dest.addWebsocket(em.websockets[id])
	ws := em.websockets[id]

	em.sendEvent(&EventPackage{
		id,
		"TRANSFEERED",
		map[string]interface{}{
			"Id":           newId,
			"EventManager": dest,
		},
	})

	delete(em.websockets, id)
	dest.blockers[newId] = em.blockers[id]

	// Wait for incomming package and than transfer listener
	go func() {
		lastEvent := <-em.transfers[id]
		lastEvent.Id = newId

		delete(em.blockers, id)
		delete(em.transfers, id)

		dest.sendEvent(lastEvent)
		dest.listen(ws, newId)
	}()
}

// Empty EventPackage for use in tight loops
func (pack *EventPackage) Clear() {
	pack.Id = 0
	pack.Event = ""
	pack.EventData = nil
}

/* Private methods */

// The actual listener
func (em *EventManager) listen(ws *websocket.Conn, id int) {
	var err error
	input := new(EventPackage)

	for err == nil {
		input.Clear()
		err = websocket.JSON.Receive(ws, input)
		input.Id = id

		// The outside world should not fire buildin events
		switch input.Event {
		case "TRANSFERRED", "CONNECTED", "DISCONNECTED":
			input.Event = strings.ToLower(input.Event)
		}

		// Check for connection transfer
		transfer, ok := em.transfers[id]
		if ok {
			transfer <- input
			return
		}

		if err == nil {
			em.sendEvent(input)
		}
	}

	ws.Close()
	delete(em.websockets, id)
	em.sendEvent(&EventPackage{id, "DISCONNECTED", err})
	em.blockers[id] <- 1 //Unblock to allow main ws handler to exit
	delete(em.blockers, id)
}

// Sending the eventPackage to the registed channels
func (em *EventManager) sendEvent(pack *EventPackage) {
	em.wLock.Add(1)
	defer em.wLock.Done()
	em.rLock.Wait()

	for _, handler := range em.eventHandlers {
		handler <- *pack
	}
}

// Register the new websocket
func (em *EventManager) addWebsocket(ws *websocket.Conn) int {
	id := len(em.websockets)
	_, ok := em.websockets[id]

	for ok {
		id += 1
		for _, ok2 := em.transfers[id]; ok2; _, ok2 = em.transfers[id] {
			id += 1
		}
		_, ok = em.websockets[id]
	}

	em.websockets[id] = ws
	em.blockers[id] = make(chan byte, 1)
	em.sendEvent(&EventPackage{id, "CONNECTED", nil})

	return id
}
