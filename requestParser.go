package main

import (
	"encoding/json"
	"errors"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Comcast/codex/blacklist"
	"github.com/Comcast/codex/cipher"
	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/semaphore"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
)

var (
	errEmptyID           = errors.New("Empty id is invalid")
	errUnexpectedWRPType = errors.New("Unexpected wrp message type")
	errTimestampString   = errors.New("timestamp couldn't be found and converted to string")
	errBlacklist         = errors.New("device is in blacklist")
)

type requestParser struct {
	encrypter       cipher.Encrypt
	blacklist       blacklist.List
	rules           []rule
	defaultTTL      time.Duration
	metadataMaxSize int
	payloadMaxSize  int
	insertQueue     chan db.Record
	maxParseWorkers int
	parseWorkers    semaphore.Interface
	wg              sync.WaitGroup
	measures        *Measures
	logger          log.Logger
}

func (r *requestParser) parseRequests(requestQueue chan wrp.Message) {
	defer r.wg.Done()
	for request := range requestQueue {
		if r.measures != nil {
			r.measures.ParsingQueue.Add(-1.0)
		}
		r.parseWorkers.Acquire()
		go r.parseRequest(request)
	}

	// Grab all the workers to make sure they are done.
	for i := 0; i < r.maxParseWorkers; i++ {
		r.parseWorkers.Acquire()
	}
}

func (r *requestParser) parseRequest(request wrp.Message) {
	defer r.parseWorkers.Release()

	rule, err := findRule(r.rules, request.Destination)
	if err != nil {
		logging.Info(r.logger).Log(logging.MessageKey(), "Could not get rule", logging.ErrorKey(), err, "destination", request.Destination)
	}

	eventType := db.ParseEventType(rule.eventType)
	record, reason, err := r.createRecord(request, rule, eventType)
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, reason).Add(1.0)
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to create record", logging.ErrorKey(), err.Error())
		return
	}

	if r.measures != nil {
		r.measures.InsertingQueue.Add(1.0)
	}
	r.insertQueue <- record
}

func (r *requestParser) createRecord(req wrp.Message, rule rule, eventType db.EventType) (db.Record, string, error) {
	var (
		err         error
		emptyRecord db.Record
		record      = db.Record{Type: eventType}
	)

	// get state and id from dest
	base, _ := path.Split(req.Destination)
	base, deviceId := path.Split(path.Base(base))
	if deviceId == "" {
		return emptyRecord, parseFailReason, emperror.WrapWith(errEmptyID, "id check failed", "request destination", req.Destination, "full message", req)
	}
	record.DeviceID = strings.ToLower(deviceId)

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
	record.BirthDate = birthDate.Unix()

	// determine ttl for deathdate
	ttl := r.defaultTTL
	if rule.ttl != 0 {
		ttl = rule.ttl
	}
	record.DeathDate = birthDate.Add(ttl).Unix()

	// store the payload if we are supposed to and it's not too big
	if !rule.storePayload || len(msg.Payload) > r.payloadMaxSize {
		msg.Payload = nil
	}

	// if metadata is too large, store a message explaining that instead of the metadata
	marshaledMetadata, err := json.Marshal(msg.Metadata)
	if err != nil {
		return emptyRecord, parseFailReason, emperror.WrapWith(err, "failed to marshal metadata to determine size", "metadata", msg.Metadata, "full message", req)
	}
	if len(marshaledMetadata) > r.metadataMaxSize {
		msg.Metadata = make(map[string]string)
		msg.Metadata["error"] = "metadata provided exceeds size limit - too big to store"
	}

	marshalledEvent, err := json.Marshal(msg)
	if err != nil {
		return emptyRecord, marshalFailReason, emperror.WrapWith(err, "failed to marshal event", "full message", req)
	}

	encyptedData, nonce, err := r.encrypter.EncryptMessage(marshalledEvent)
	if err != nil {
		return emptyRecord, encryptFailReason, emperror.WrapWith(err, "failed to encrypt message")
	}
	record.Data = encyptedData
	record.Nonce = nonce

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
