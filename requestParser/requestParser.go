package requestParser

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/kit/metrics/provider"

	"github.com/Comcast/codex-svalinn/rules"

	"github.com/Comcast/codex/blacklist"
	"github.com/Comcast/codex/cipher"
	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/semaphore"
	"github.com/Comcast/wrp-go/wrp"
	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
)

var (
	errEmptyID           = errors.New("empty id is invalid")
	errUnexpectedWRPType = errors.New("unexpected wrp message type")
	errTimestampString   = errors.New("timestamp couldn't be found and converted to string")
	errFutureBirthdate   = errors.New("birthdate is too far in the future")
	errExpired           = errors.New("deathdate has passed")
	errBlacklist         = errors.New("device is in blacklist")
	errQueueFull         = errors.New("Queue Full")

	defaultLogger = log.NewNopLogger()
)

const (
	defaultTTL          = time.Duration(5) * time.Minute
	minMaxWorkers       = 5
	defaultMinQueueSize = 5
)

type inserter interface {
	Insert(record db.Record)
}

type Config struct {
	MetadataMaxSize int
	PayloadMaxSize  int
	QueueSize       int
	MaxWorkers      int
	DefaultTTL      time.Duration
	RegexRules      []rules.RuleConfig
}

type RequestParser struct {
	encrypter    cipher.Encrypt
	blacklist    blacklist.List
	inserter     inserter
	rules        rules.Rules
	requestQueue chan wrp.Message
	parseWorkers semaphore.Interface
	wg           sync.WaitGroup
	measures     *Measures
	logger       log.Logger
	config       Config
}

func NewRequestParser(config Config, logger log.Logger, metricsRegistry provider.Provider, inserter inserter, blacklist blacklist.List, encrypter cipher.Encrypt) (*RequestParser, error) {
	if encrypter == nil {
		return nil, errors.New("no encrypter")
	}
	if blacklist == nil {
		return nil, errors.New("no blacklist")
	}
	if inserter == nil {
		return nil, errors.New("no inserter")
	}
	rules, err := rules.NewRules(config.RegexRules)
	if err != nil {
		return nil, emperror.Wrap(err, "failed to create rules from config")
	}

	if config.DefaultTTL == 0 {
		config.DefaultTTL = defaultTTL
	}
	if config.MetadataMaxSize < 0 {
		config.MetadataMaxSize = 0
	}
	if config.PayloadMaxSize < 0 {
		config.PayloadMaxSize = 0
	}
	if config.MaxWorkers < minMaxWorkers {
		config.MaxWorkers = minMaxWorkers
	}

	if config.QueueSize < defaultMinQueueSize {
		config.QueueSize = defaultMinQueueSize
	}
	if logger == nil {
		logger = defaultLogger
	}

	var measures *Measures
	if metricsRegistry != nil {
		measures = NewMeasures(metricsRegistry)
	}
	queue := make(chan wrp.Message, config.QueueSize)
	workers := semaphore.New(config.MaxWorkers)
	r := RequestParser{
		config:       config,
		logger:       logger,
		measures:     measures,
		parseWorkers: workers,
		requestQueue: queue,
		inserter:     inserter,
		rules:        rules,
		blacklist:    blacklist,
		encrypter:    encrypter,
	}

	return &r, nil
}

func (r *RequestParser) Start() {
	r.wg.Add(1)
	go r.parseRequests()
}

func (r *RequestParser) Parse(message wrp.Message) (err error) {
	select {
	case r.requestQueue <- message:
		if r.measures != nil {
			r.measures.ParsingQueue.Add(1.0)
		}
	default:
		if r.measures != nil {
			r.measures.DroppedEventsCount.With(reasonLabel, queueFullReason).Add(1.0)
		}
		err = errQueueFull
	}
	return
}

func (r *RequestParser) Stop() {
	close(r.requestQueue)
	r.wg.Wait()
}

func (r *RequestParser) parseRequests() {
	defer r.wg.Done()
	for request := range r.requestQueue {
		if r.measures != nil {
			r.measures.ParsingQueue.Add(-1.0)
		}
		r.parseWorkers.Acquire()
		go r.parseRequest(request)
	}

	// Grab all the workers to make sure they are done.
	for i := 0; i < r.config.MaxWorkers; i++ {
		r.parseWorkers.Acquire()
	}
}

func (r *RequestParser) parseRequest(request wrp.Message) {
	defer r.parseWorkers.Release()

	rule, err := r.rules.FindRule(request.Destination)
	if err != nil {
		logging.Info(r.logger).Log(logging.MessageKey(), "Could not get rule", logging.ErrorKey(), err, "destination", request.Destination)
	}

	eventType := db.Default
	if rule != nil {
		eventType = db.ParseEventType(rule.EventType())
	}
	record, reason, err := r.createRecord(request, rule, eventType)
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, reason).Add(1.0)
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to create record", logging.ErrorKey(), err.Error())
		return
	}

	r.inserter.Insert(record)
}

func (r *RequestParser) createRecord(req wrp.Message, rule *rules.Rule, eventType db.EventType) (db.Record, string, error) {
	var (
		err         error
		emptyRecord db.Record
		record      = db.Record{Type: eventType}
	)

	if eventType == db.State {
		// get state and id from dest if this is a state event
		base, _ := path.Split(req.Destination)
		base, deviceId := path.Split(path.Base(base))
		if deviceId == "" {
			return emptyRecord, parseFailReason, emperror.WrapWith(errEmptyID, "id check failed", "request destination", req.Destination, "full message", req)
		}
		record.DeviceID = strings.ToLower(deviceId)
	} else {
		if req.Source == "" {
			return emptyRecord, parseFailReason, emperror.WrapWith(errEmptyID, "id check failed", "request Source", req.Source, "full message", req)
		}
		record.DeviceID = strings.ToLower(req.Source)
	}

	if reason, ok := r.blacklist.InList(record.DeviceID); ok {
		return emptyRecord, blackListReason, emperror.With(errBlacklist, "reason", reason)
	}

	// verify wrp is the right type
	msg := req
	switch msg.Type {
	case wrp.SimpleEventMessageType:

	default:
		return emptyRecord, parseFailReason, emperror.WrapWith(errUnexpectedWRPType, "message type check failed", "type", msg.Type, "full message", req)
	}

	// get timestamp from wrp payload
	birthDate, ok := getBirthDate(msg.Payload)
	if !ok {
		birthDate = time.Now()
	}
	record.BirthDate = birthDate.UnixNano()

	if birthDate.After(time.Now().Add(time.Hour)) {
		return emptyRecord, invalidBirthdateReason, emperror.WrapWith(errFutureBirthdate, "invalid birthdate", "birthdate", birthDate.String())
	}

	// determine ttl for deathdate
	ttl := r.config.DefaultTTL
	if rule != nil && rule.TTL() != 0 {
		ttl = rule.TTL()
	}

	deathDate := birthDate.Add(ttl)
	if time.Now().After(deathDate) {
		return emptyRecord, expiredReason, emperror.WrapWith(errExpired, "event is already expired", "deathdate", deathDate.String())
	}
	record.DeathDate = deathDate.UnixNano()

	// store the payload if we are supposed to and it's not too big
	storePayload := false
	if rule != nil {
		storePayload = rule.StorePayload()
	}
	if !storePayload || len(msg.Payload) > r.config.PayloadMaxSize {
		msg.Payload = nil
	}

	// if metadata is too large, store a message explaining that instead of the metadata
	marshaledMetadata, err := json.Marshal(msg.Metadata)
	if err != nil {
		return emptyRecord, parseFailReason, emperror.WrapWith(err, "failed to marshal metadata to determine size", "metadata", msg.Metadata, "full message", req)
	}
	if len(marshaledMetadata) > r.config.MetadataMaxSize {
		msg.Metadata = make(map[string]string)
		msg.Metadata["error"] = "metadata provided exceeds size limit - too big to store"
	}

	var buffer bytes.Buffer
	msgEncoder := wrp.NewEncoder(&buffer, wrp.Msgpack)
	err = msgEncoder.Encode(&msg)
	if err != nil {
		return emptyRecord, marshalFailReason, emperror.WrapWith(err, "failed to marshal event", "full message", req)
	}

	encyptedData, nonce, err := r.encrypter.EncryptMessage(buffer.Bytes())
	if err != nil {
		return emptyRecord, encryptFailReason, emperror.WrapWith(err, "failed to encrypt message")
	}
	record.Data = encyptedData
	record.Nonce = nonce
	record.Alg = string(r.encrypter.GetAlgorithm())
	record.KID = r.encrypter.GetKID()

	return record, "", nil
}

func getBirthDate(payload []byte) (time.Time, bool) {
	p := make(map[string]interface{})
	if payload == nil || len(payload) == 0 {
		return time.Time{}, false
	}
	err := json.Unmarshal(payload, &p)
	if err != nil {
		return time.Time{}, false
	}

	// parse the time from the payload
	timeString, ok := p["ts"].(string)
	if !ok {
		return time.Time{}, false
	}
	birthDate, err := time.Parse(time.RFC3339Nano, timeString)
	if err != nil {
		return time.Time{}, false
	}
	return birthDate, true
}
