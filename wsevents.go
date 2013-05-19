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
	Id           int
	Event        string
	EventData    interface{}
	EventManager *EventManager
	handlerCount int
	handled      bool
	counterMutex sync.Mutex
}

type EventManager struct {
	eventHandlers []chan *EventPackage
	events        map[string][]chan *EventPackage
	websockets    map[int]*websocket.Conn
	transfers     map[int]chan *EventPackage
	blockers      map[int]chan byte
	eventMutex    sync.RWMutex // For eventHandlers & events
	wsMutex       sync.RWMutex // For websockets & blockers
	transferMutex sync.RWMutex
}

// Constructor
func NewEventManager() (em *EventManager) {
	em = &EventManager{
		events:     make(map[string][]chan *EventPackage),
		websockets: make(map[int]*websocket.Conn),
		transfers:  make(map[int]chan *EventPackage),
		blockers:   make(map[int]chan byte),
	}
	go eventHandler(em.RegisterHandler())

	return
}

// Sending the eventPackage to the registed channels in events
func eventHandler(c chan *EventPackage) {
	for {
		pack := <-c
		pack.EventManager.eventMutex.RLock()
		if channels, ok := pack.EventManager.events[pack.Event]; ok {
			pack.Handled(true)

			for _, channel := range channels {
				go func() {
					channel <- pack
					//Fixme: handle closed channels e.g. unregisterd event handlers
				}()
			}
		} else {
			pack.Handled(false)
		}

		pack.EventManager.eventMutex.RUnlock()
	}
}

// Register eventhandler
func (em *EventManager) RegisterHandler() (receiver chan *EventPackage) {
	em.eventMutex.Lock()
	defer em.eventMutex.Unlock()

	receiver = make(chan *EventPackage, 50)
	em.eventHandlers = append(em.eventHandlers, receiver)
	return
}

// Register event
func (em *EventManager) RegisterEvent(eventName string) (receiver chan *EventPackage) {
	channels, ok := em.events[eventName]

	// Allocate space for the new channel in slice
	if ok == false {
		channels = make([]chan *EventPackage, 1)
	} else {
		channels = append(channels, make(chan *EventPackage, 1))
	}

	receiver = make(chan *EventPackage, 50) // Create the channel
	channels[len(channels)-1] = receiver
	em.events[eventName] = channels // Add the new slice to the map

	return
}

// Remove eventhandler
func (em *EventManager) Unregister(receiver chan *EventPackage) {
	em.eventMutex.Lock()
	defer em.eventMutex.Unlock()

	// Remove eventHandler
	for i, c := range em.eventHandlers {
		if receiver == c {
			em.eventHandlers[i], em.eventHandlers = em.eventHandlers[len(em.eventHandlers)-1], em.eventHandlers[:len(em.eventHandlers)-1]
			close(c)
			return
		}
	}

	// Remove event
	for event, channels := range em.events {
		// if last handler in event
		if (len(channels) == 1) && (receiver == channels[0]) {
			close(channels[0])
			delete(em.events, event)
			return
		} //else

		for i, c := range channels {
			if receiver == c {
				channels[i], channels = channels[len(channels)-1], channels[:len(channels)-1]
				em.events[event] = channels
				close(c)
				return
			}
		}
	}
}

// Register and linsten on websocket
func (em *EventManager) Listen(ws *websocket.Conn) {
	id := em.addWebsocket(ws)
	em.listen(ws, id) // cal the actual listerer

	em.wsMutex.RLock()
	blocker, ok := em.blockers[id]
	em.wsMutex.RUnlock()
	if ok {
		<-blocker
	}
}

// Send something to multiple connections
func (em *EventManager) Dispatch(d Dispatcher) {
	em.wsMutex.RLock()
	defer em.wsMutex.RUnlock()

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
	em.wsMutex.RLock()
	defer em.wsMutex.RUnlock()

	err = ErrConnNotFound

	if ws, ok := em.websockets[id]; ok {
		err = websocket.JSON.Send(ws, pack)
	}

	return
}

// Transfer connection to dest EventManager
func (em *EventManager) Transfer(id int, dest *EventManager) {
	em.transferMutex.Lock()
	em.transfers[id] = make(chan *EventPackage)
	em.transferMutex.Unlock()

	em.wsMutex.RLock()
	newId := dest.addWebsocket(em.websockets[id])
	ws := em.websockets[id]
	em.wsMutex.RUnlock()

	em.sendEvent(&EventPackage{
		Id:    id,
		Event: "TRANSFERRED",
		EventData: map[string]interface{}{
			"Id":           newId,
			"EventManager": dest,
		},
		EventManager: em,
		handled:      true,
	})

	em.wsMutex.Lock()
	delete(em.websockets, id)
	em.wsMutex.Unlock()
	dest.wsMutex.Lock()
	dest.blockers[newId] = em.blockers[id]
	dest.wsMutex.Unlock()

	// Wait for incomming package and than transfer listener
	go func() {
		em.transferMutex.RLock()
		c := em.transfers[id]
		em.transferMutex.RUnlock()
		lastEvent := <-c
		lastEvent.Id = newId
		lastEvent.EventManager = dest

		em.wsMutex.Lock()
		delete(em.blockers, id)
		em.wsMutex.Unlock()
		em.transferMutex.Lock()
		delete(em.transfers, id)
		em.transferMutex.Unlock()

		dest.sendEvent(lastEvent)
		dest.listen(ws, newId)
	}()
}

// Indicate wether we handled the event Should only(and always) be called by registered event handlers.
func (pack *EventPackage) Handled(handled bool) {
	pack.counterMutex.Lock()
	defer pack.counterMutex.Unlock()
	pack.handlerCount--

	if handled {
		pack.handled = true
		return
	}

	if !pack.handled && (pack.handlerCount == 0) {
		pack.EventManager.sendEvent(&EventPackage{
			Id:           pack.Id,
			Event:        "DEFAULT",
			EventData:    pack,
			EventManager: pack.EventManager,
			handled:      true,
		})
	}
}

// Implement Dispatcher.Match
func (pack *EventPackage) Match(id int) bool {
	return true
}

// Implement Dispatcher.BuildPackage
func (pack *EventPackage) BuildPackage(id int) interface{} {
	return &map[string]interface{}{
		"Id":        pack.Id,
		"Event":     pack.Event,
		"EventData": pack.EventData,
	}
}

// Implement Dispatcher.Error
func (pack *EventPackage) Error(err error, dataSend interface{}) {}

/* Private methods */

// The actual listener
func (em *EventManager) listen(ws *websocket.Conn, id int) {
	var err error

	for err == nil {
		input := new(EventPackage)
		err = websocket.JSON.Receive(ws, input)
		input.Id = id
		input.EventManager = em
		em.eventMutex.RLock()
		input.handlerCount = len(em.eventHandlers)
		em.eventMutex.RUnlock()

		// The outside world should not fire buildin events
		switch input.Event {
		case "TRANSFERRED", "CONNECTED", "DISCONNECTED", "DEFAULT":
			input.Event = strings.ToLower(input.Event)
		}

		// Check for connection transfer
		em.transferMutex.RLock()
		transfer, ok := em.transfers[id]
		em.transferMutex.RUnlock()
		if ok {
			transfer <- input
			return
		}

		if err == nil {
			em.sendEvent(input)
		}
	}

	em.wsMutex.Lock()
	delete(em.websockets, id)
	em.blockers[id] <- 1 //Unblock to allow main ws handler to exit
	delete(em.blockers, id)
	em.wsMutex.Unlock()

	em.sendEvent(&EventPackage{
		Id:           id,
		Event:        "DISCONNECTED",
		EventData:    err,
		EventManager: em,
		handled:      true,
	})
}

// Sending the eventPackage to the registed channels
func (em *EventManager) sendEvent(pack *EventPackage) {
	em.eventMutex.RLock()
	defer em.eventMutex.RUnlock()

	for _, handler := range em.eventHandlers {
		handler <- pack
	}
}

// Register the new websocket
func (em *EventManager) addWebsocket(ws *websocket.Conn) int {
	em.wsMutex.Lock()
	id := len(em.websockets)
	_, ok := em.websockets[id]

	for ok {
		id += 1

		em.transferMutex.RLock()
		for _, inTransit := em.transfers[id]; inTransit; _, inTransit = em.transfers[id] {
			id += 1
		}
		em.transferMutex.RUnlock()

		_, ok = em.websockets[id]
	}

	em.websockets[id] = ws
	em.blockers[id] = make(chan byte, 1)
	em.wsMutex.Unlock()
	em.sendEvent(&EventPackage{
		Id:           id,
		Event:        "CONNECTED",
		EventManager: em,
		handled:      true,
	})

	return id
}
