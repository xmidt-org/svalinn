package eventparser

import (
	"errors"
	"time"

	db "github.com/xmidt-org/codex-db"
	"github.com/xmidt-org/wrp-go/v3"
	wrpparser "github.com/xmidt-org/wrp-listener/wrpParser"
)

const (
	defaultTTL = time.Duration(5) * time.Minute
)

type EventParser struct {
	classifier wrpparser.Classifier
	finders    map[db.EventType]labelResults
}

type labelResults struct {
	storePayload bool
	ttl          time.Duration
	finder       wrpparser.DeviceFinder
}

type Result struct {
	Label        db.EventType
	DeviceID     string
	StorePayload bool
	TTL          time.Duration
}

func (p *EventParser) Parse(msg *wrp.Message) (*Result, error) {
	eventType := db.Default
	
	if label, ok := p.classifier.Label(msg); ok {
		eventType = db.ParseEventType(label)
	}

	// get the finder for the event type, and if that doesn't work, get the
	// default finder and results.
	lr, ok := p.finders[eventType]
	if !ok {
		eventType := db.Default
		lr, ok = p.finders[eventType]
		if !ok {
			return nil, errors.New("this is a serious problem")
		}
	}

	id, err := lr.finder.FindDeviceID(msg)
	if err != nil {
		return nil, errors.New("some error")
	}
	return &Result{
		Label:        eventType,
		DeviceID:     id,
		StorePayload: lr.storePayload,
		TTL:          lr.ttl,
	}, nil

}

// Option is a function used to configure the StrParser.
type Option func(*EventParser)

// WithDeviceFinder adds a DeviceFinder that the StrParser will use to find the
// device id of a wrp message.  Each DeviceFinder is associated with a label.
// If the label already has a DeviceFinder associated with it, it will be
// replaced by the new one (as long as the DeviceFinder is not nil).
func WithDeviceFinder(label string, finder wrpparser.DeviceFinder, storePayload bool, ttl time.Duration) Option {
	return func(parser *EventParser) {
		if finder != nil {
			lr := labelResults{
				finder:       finder,
				storePayload: storePayload,
				ttl:          ttl,
			}
			if lr.ttl == 0 {
				lr.ttl = defaultTTL
			}
			parser.finders[db.ParseEventType(label)] = lr
		}
	}
}

func New(classifier wrpparser.Classifier, options ...Option) (*EventParser, error) {
	if classifier == nil {
		return nil, errors.New("some error")
	}

	p := &EventParser{
		classifier: classifier,
		finders:    map[db.EventType]labelResults{},
	}

	for _, o := range options {
		o(p)
	}

	// set up a default if it doesn't exist
	if _, ok := p.finders[db.Default]; !ok {
		defaultFinder := wrpparser.FieldFinder{Field: wrpparser.Source}
		p.finders[db.Default] = labelResults{
			finder:       &defaultFinder,
			storePayload: false,
			ttl:          defaultTTL,
		}
	}

	return p, nil
}
