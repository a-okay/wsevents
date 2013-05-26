// Copyright 2013 Karel van IJperen. All rights reserved.
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

type TransferData struct {
	Id   int
	Src  *EventManager
	Dest *EventManager
}

type EventManager struct {
	eventHandler
	eventHandlers []chan *EventPackage
	connections   map[int]*connectionData
	// Command channels
	addConnection     chan *connectionData
	removeConnection  chan int
	registerHandler   chan chan *EventPackage
	unregisterHandler chan chan *EventPackage
	sendEvent         chan *EventPackage
	send              chan *sendData
	dispatch          chan Dispatcher
	transfer          chan *TransferData
}

type eventHandler struct {
	events map[string][]chan *EventPackage
	// Command channels
	registerEvent   chan *eventData
	unregisterEvent chan chan *EventPackage
}

type connectionData struct {
	ws        *websocket.Conn
	incomming chan *EventPackage
	stop      chan byte
	td        *TransferData
}

type sendData struct {
	id   int
	pack interface{}
	err  chan error
}

type eventData struct {
	eventName string
	receiver  chan *EventPackage
}

// Constructor
func NewEventManager() (m *EventManager) {
	m = &EventManager{
		connections:       make(map[int]*connectionData),
		addConnection:     make(chan *connectionData, 10),
		removeConnection:  make(chan int, 10),
		registerHandler:   make(chan chan *EventPackage, 10),
		unregisterHandler: make(chan chan *EventPackage, 10),
		sendEvent:         make(chan *EventPackage, 10),
		send:              make(chan *sendData, 10),
		dispatch:          make(chan Dispatcher, 10),
		transfer:          make(chan *TransferData, 10),
	}

	m.eventHandler = eventHandler{
		events:          make(map[string][]chan *EventPackage),
		registerEvent:   make(chan *eventData, 10),
		unregisterEvent: make(chan chan *EventPackage, 10),
	}

	go m.worker()
	go m.eventHandler.worker(m.RegisterHandler())

	return
}

// Add connection and listen on websocket for incomming events
func (m *EventManager) Listen(ws *websocket.Conn) (err error) {
	c := make(chan *EventPackage, 10)
	m.addConnection <- &connectionData{
		ws:        ws,
		incomming: c,
	}

	for err == nil {
		pack := new(EventPackage)
		err = websocket.JSON.Receive(ws, pack)

		if err == nil {
			c <- pack
		}
	}

	close(c)
	return
}

// Coupling mechanism between Listen and EventManager
func (m *EventManager) incommingHandler(id int, incomming chan *EventPackage, stop chan byte) {
	for {
		select {
		case <-stop:
			return
		case pack, ok := <-incomming:
			if !ok {
				m.removeConnection <- id
				<-stop // Make sure we read stop
				return
			}

			// The outside world should not fire buildin events
			switch pack.Event {
			case "TRANSFERRED", "CONNECTED", "DISCONNECTED", "DEFAULT":
				pack.Event = strings.ToLower(pack.Event)
			}

			pack.Id = id
			pack.EventManager = m
			m.sendEvent <- pack
		}
	}
}

func (m *EventManager) RegisterHandler() (receiver chan *EventPackage) {
	receiver = make(chan *EventPackage, 10)
	m.registerHandler <- receiver
	return
}

func (m *EventManager) Unregister(receiver chan *EventPackage) {
	m.unregisterHandler <- receiver
	m.unregisterEvent <- receiver
}

func (m *EventManager) Send(id int, pack interface{}) (err error) {
	errChan := make(chan error)
	m.send <- &sendData{
		id:   id,
		pack: pack,
		err:  errChan,
	}

	return <-errChan
}

// Send something to multiple connections
func (m *EventManager) Dispatch(d Dispatcher) {
	m.dispatch <- d
}

// Transfer connection to dest
func (m *EventManager) Transfer(id int, dest *EventManager) {
	m.transfer <- &TransferData{id, m, dest}
}

// Listen on command channels
func (m *EventManager) worker() {
	for{
		select {
		case conn := <-m.addConnection:
			m.addConn(conn)
		case id := <-m.removeConnection:
			m.rmConn(id)
		case receiver := <-m.registerHandler:
			m.eventHandlers = append(m.eventHandlers, receiver)
		case receiver := <-m.unregisterHandler:
			for i, c := range m.eventHandlers {
				if receiver == c {
					m.eventHandlers[i], m.eventHandlers = m.eventHandlers[len(m.eventHandlers)-1], m.eventHandlers[:len(m.eventHandlers)-1]
					close(c)
				}
			}
		case pack := <-m.sendEvent:
			pack.handlerCount = len(m.eventHandlers)
			for _, handler := range m.eventHandlers {
				go func(handler chan *EventPackage) {
					handler <- pack
				}(handler)
			}
		case sd := <-m.send:
			err := ErrConnNotFound

			if conn, ok := m.connections[sd.id]; ok {
				err = websocket.JSON.Send(conn.ws, sd.pack)
			}
			sd.err <- err
		case d := <-m.dispatch:
			for id, conn := range m.connections {
				if d.Match(id) {
					go func(id int, conn *connectionData) {
						pack := d.BuildPackage(id)
						err := websocket.JSON.Send(conn.ws, pack)
						if err != nil {
							d.Error(err, pack)
						}
					}(id, conn)
				}
			}
		case td := <-m.transfer:
			conn := m.connections[td.Id]
			conn.td = td
			conn.stop <- 1
			conn.td.Dest.addConnection <- conn
			delete(m.connections, td.Id)
		}
	}
}

func (m *EventManager) addConn(conn *connectionData) {
	conn.stop = make(chan byte)
	id := len(m.connections)
	for ok := true; ok; id += 1 {
		_, ok = m.connections[id]
	}

	m.connections[id] = conn
	go m.incommingHandler(id, conn.incomming, conn.stop)
	m.sendEvent <- &EventPackage{
		Id:           id,
		Event:        "CONNECTED",
		EventManager: m,
		EventData:    conn.td,
		handled:      true,
	}
	if conn.td != nil {
		conn.td.Dest.sendEvent <- &EventPackage{
			Id:           id,
			Event:        "TRANSFERRED",
			EventData:    conn.td,
			EventManager: m,
			handled:      true,
		}
	}
}

func (m *EventManager) rmConn(id int) {
	m.connections[id].stop <- 1
		m.sendEvent <- &EventPackage{
			Id:           id,
			Event:        "DISCONNECTED",
			EventManager: m,
			EventData:    m.connections[id].ws.Close(),
			handled:      true,
		}
	delete(m.connections, id)
}

func (h *eventHandler) worker(incomming chan *EventPackage) {
	for {
		select {
		case ed := <-h.registerEvent:
			h.events[ed.eventName] = append(h.events[ed.eventName], ed.receiver)
		case receiver := <-h.unregisterEvent:
			h.unregEvent(receiver)
		case pack := <-incomming:
			if channels, ok := h.events[pack.Event]; ok {
				pack.Handled(true)

				for _, channel := range channels {
					go func(channel chan *EventPackage) {
						channel <- pack
					}(channel)
				}
			} else {
				pack.Handled(false)
			}
		}
	}
}

// Register event
func (h *eventHandler) RegisterEvent(eventName string) (receiver chan *EventPackage) {
	receiver = make(chan *EventPackage, 10) // Create the channel
	h.registerEvent <- &eventData{eventName, receiver}
	return
}

func (h *eventHandler) unregEvent(receiver chan *EventPackage) {
	for eventName, channels := range h.events {
		// if last handler in event
		if (len(channels) == 1) && (receiver == channels[0]) {
			close(channels[0])
			delete(h.events, eventName)
			return
		} //else

		for i, c := range channels {
			if receiver == c {
				channels[i], channels = channels[len(channels)-1], channels[:len(channels)-1]
				h.events[eventName] = channels
				close(c)
				return
			}
		}
	}
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
		pack.EventManager.sendEvent <- &EventPackage{
			Id:           pack.Id,
			Event:        "DEFAULT",
			EventData:    pack,
			EventManager: pack.EventManager,
			handled:      true,
		}
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
