Event interface to websockets in Go.
===

## Install

Use the go *get* tool to download and install. If you have not already done so first install websocket:

    go get code.google.com/p/go.net/websocket
    go get github.com/kvij/wsevents

## Usage

### For a working example see example/example.go...

Obtaining an EventManager:

    var EventManager *wsevents.EventManager = wsevents.NewEventManager()

Start listening for incomming events on a websocket:

    func wsHandler(ws *websocket.Conn) {
        EventManager.Listen(ws)
    }

Obtaining a channel for a single event (Mostly used in OO):

    var EventChannel chan *EventPackage = EventManager.RegisterEvent("EventName")

Obtaining a channel that receives all events:

    var HandlerChannel chan *EventPackage = EventManager.RegisterHandler()

Sending something to a specific websocket:

    pack <- EventChannel
    pack.EventManager.Send(pack.Id, "Some Message")

Sending something to all (Or selection created by Dispatcher.Match) websockets in EventManager:

    pack <- EventChannel
    pack.EventManager.Dispatch(pack) // *EventPackage implements Dispatcher interface

Removing an Event- or HandlerChannel:

    EventManager.Unregister(HandlerChannel)

## Known issues / Fixme

* If a handler changes the EventPackage obtained trough its channel other handlers might be affected.
* To much use of mutexes. Could probably be changed to "share by communicating".
