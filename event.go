package echo

import (
	"github.com/admpub/events"
)

func On(name string, handlers ...events.Listener) events.Emitterer {
	return events.Default.On(name, handlers...)
}

func OnCallback(name string, cb func(events.Event) error) events.Emitterer {
	return events.Default.On(name, events.Callback(cb))
}

func OnStream(name string, ch chan events.Event) events.Emitterer {
	return events.Default.On(name, events.Stream(ch))
}

func Off(name string) events.Emitterer {
	return events.Default.Off(name)
}

func AddListener(listener events.Listener, names ...string) events.Emitterer {
	events.Default.AddEventListener(listener, names...)
	return events.Default
}

func RemoveListener(listener events.Listener) events.Emitterer {
	events.Default.RemoveEventListener(listener)
	return events.Default
}

func Fire(e interface{}) error {
	return events.Default.Fire(e)
}

func HasEvent(name string) bool {
	return events.Default.HasEvent(name)
}

func NewEvent(data interface{}, options ...events.EventOption) events.Event {
	return events.New(data, options...)
}
