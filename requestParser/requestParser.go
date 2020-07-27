package requestParser

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/go-kit/kit/metrics/provider"

	eventparser "github.com/xmidt-org/svalinn/eventParser"
	wrpparser "github.com/xmidt-org/wrp-listener/wrpParser"

	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
	db "github.com/xmidt-org/codex-db"
	"github.com/xmidt-org/codex-db/batchInserter"
	"github.com/xmidt-org/codex-db/blacklist"
	"github.com/xmidt-org/voynicrypto"
	"github.com/xmidt-org/webpa-common/logging"
	"github.com/xmidt-org/webpa-common/semaphore"
	"github.com/xmidt-org/wrp-go/v3"
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
	minMaxWorkers       = 5
	defaultMinQueueSize = 5
)

type TimeTracker interface {
	TrackTime(time.Duration)
}

type inserter interface {
	Insert(record batchInserter.RecordWithTime) error
}

type Config struct {
	MetadataMaxSize int
	PayloadMaxSize  int
	QueueSize       int
	MaxWorkers      int
	Labels          map[string]LabelConfig
}

type LabelConfig struct {
	Regex          string
	StorePayload   bool
	TTL            time.Duration
	DeviceLocation FinderConfig
}

type FinderConfig struct {
	Field      string
	Regex      string
	RegexLabel string
}

type RequestParser struct {
	encrypter    voynicrypto.Encrypt
	blacklist    blacklist.List
	inserter     inserter
	timeTracker  TimeTracker
	eventParser  *eventparser.EventParser
	requestQueue chan WrpWithTime
	parseWorkers semaphore.Interface
	wg           sync.WaitGroup
	measures     *Measures
	logger       log.Logger
	config       Config
	currTime     func() time.Time
}

type WrpWithTime struct {
	Message   wrp.Message
	Beginning time.Time
}

func NewRequestParser(config Config, logger log.Logger, metricsRegistry provider.Provider, inserter inserter, blacklist blacklist.List, encrypter voynicrypto.Encrypt, timeTracker TimeTracker) (*RequestParser, error) {
	if encrypter == nil {
		return nil, errors.New("no encrypter")
	}
	if blacklist == nil {
		return nil, errors.New("no blacklist")
	}
	if inserter == nil {
		return nil, errors.New("no inserter")
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

	// set up eventParser
	var os []eventparser.Option
	var cs []wrpparser.Classifier
	for k, v := range config.Labels {
		eventType := db.ParseEventType(k)
		if eventType != db.Default {
			c, err := wrpparser.NewRegexpClassifierFromStr(eventType.String(), v.Regex, wrpparser.Destination)
			if err != nil {
				// log error
			} else {
				cs = append(cs, c)
			}
		}
		f, err := wrpparser.NewRegexpFinderFromStr(
			wrpparser.GetField(v.DeviceLocation.Field),
			v.DeviceLocation.Regex,
			v.DeviceLocation.RegexLabel)
		if err != nil {
			// log error
		} else {
			os = append(os, eventparser.WithDeviceFinder(eventType.String(), f, v.StorePayload, v.TTL))
		}
	}
	c, err := wrpparser.NewMultClassifier(cs...)
	if err != nil {
		return nil, errors.New("some error")
	}
	eventParser, err := eventparser.New(c, os...)
	if err != nil {
		return nil, errors.New("some error")
	}

	var measures *Measures
	if metricsRegistry != nil {
		measures = NewMeasures(metricsRegistry)
	}
	queue := make(chan WrpWithTime, config.QueueSize)
	workers := semaphore.New(config.MaxWorkers)
	r := RequestParser{
		config:       config,
		logger:       logger,
		measures:     measures,
		parseWorkers: workers,
		requestQueue: queue,
		inserter:     inserter,
		eventParser:  eventParser,
		blacklist:    blacklist,
		encrypter:    encrypter,
		currTime:     time.Now,
		timeTracker:  timeTracker,
	}

	return &r, nil
}

func (r *RequestParser) Start() {
	r.wg.Add(1)
	go r.parseRequests()
}

func (r *RequestParser) Parse(wrpWithTime WrpWithTime) (err error) {
	select {
	case r.requestQueue <- wrpWithTime:
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

func (r *RequestParser) parseRequest(request WrpWithTime) {
	defer r.parseWorkers.Release()

	result, err := r.eventParser.Parse(&request.Message)
	if err != nil || result == nil {
		logging.Info(r.logger).Log(logging.MessageKey(), "Could not parse event",
			logging.ErrorKey(), err, "destination", request.Message.Destination)
	}

	record, reason, err := r.createRecord(request.Message, result)
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, reason).Add(1.0)
		if reason == blackListReason {
			logging.Info(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
				"Failed to create record", logging.ErrorKey(), err.Error())
			r.timeTracker.TrackTime(time.Now().Sub(request.Beginning))
			return
		}
		logging.Warn(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to create record", logging.ErrorKey(), err.Error())
		r.timeTracker.TrackTime(time.Now().Sub(request.Beginning))
		return
	}

	err = r.inserter.Insert(batchInserter.RecordWithTime{Record: record, Beginning: request.Beginning})
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, insertFailReason).Add(1.0)
		logging.Warn(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to insert record", logging.ErrorKey(), err.Error())
	}
}

func (r *RequestParser) createRecord(req wrp.Message, result *eventparser.Result) (db.Record, string, error) {
	var (
		err         error
		emptyRecord db.Record
		record      = db.Record{
			Type:     result.Label,
			DeviceID: result.DeviceID}
	)

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

	now := r.currTime()

	// get timestamp from wrp payload
	birthDate, ok := getBirthDate(msg.Payload)
	if !ok {
		birthDate = now
	}
	record.BirthDate = birthDate.UnixNano()

	if birthDate.After(now.Add(time.Hour)) {
		return emptyRecord, invalidBirthdateReason, emperror.WrapWith(errFutureBirthdate, "invalid birthdate", "birthdate", birthDate.String())
	}

	deathDate := birthDate.Add(result.TTL)
	if now.After(deathDate) {
		return emptyRecord, expiredReason, emperror.WrapWith(errExpired, "event is already expired", "deathdate", deathDate.String())
	}
	record.DeathDate = deathDate.UnixNano()

	// store the payload if we are supposed to and it's not too big
	if !result.StorePayload || len(msg.Payload) > r.config.PayloadMaxSize {
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
