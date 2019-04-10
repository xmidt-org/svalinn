/**
 * Copyright 2019 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Comcast/codex/cipher"
	"io/ioutil"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Comcast/webpa-common/semaphore"

	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
)

var (
	errEmptyID           = errors.New("Empty id is invalid")
	errUnexpectedWRPType = errors.New("Unexpected wrp message type")
	errTimestampString   = errors.New("timestamp couldn't be found and converted to string")
)

type RequestHandler struct {
	inserter         db.RetryInsertService
	updater          db.RetryUpdateService
	logger           log.Logger
	encypter         cipher.Enrypt
	rules            []rule
	metadataMaxSize  int
	payloadMaxSize   int
	defaultTTL       time.Duration
	insertQueue      chan db.Record
	maxParseWorkers  int
	parseWorkers     semaphore.Interface
	maxInsertWorkers int
	insertWorkers    semaphore.Interface
	maxBatchSize     int
	maxBatchWaitTime time.Duration
	wg               sync.WaitGroup
	measures         *Measures
}

func (r *RequestHandler) handlePruning(quit chan struct{}, interval time.Duration) {
	defer r.wg.Done()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-quit:
			return
		case <-t.C:
			r.pruneDevice()
		}
	}
}

func (r *RequestHandler) pruneDevice() {
	err := r.updater.PruneRecords(time.Now().Unix())
	if err != nil {
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to update event history", logging.ErrorKey(), err.Error())
		return
	}
	logging.Debug(r.logger).Log(logging.MessageKey(), "Successfully pruned events")
	return
}

func (r *RequestHandler) handleRequests(requestQueue chan wrp.Message) {
	defer r.wg.Done()
	for request := range requestQueue {
		if r.measures != nil {
			r.measures.ParsingQueue.Add(-1.0)
		}
		r.parseWorkers.Acquire()
		go r.handleRequest(request)
	}

	// Grab all the workers to make sure they are done.
	for i := 0; i < r.maxParseWorkers; i++ {
		r.parseWorkers.Acquire()
	}
}

func (r *RequestHandler) handleRequest(request wrp.Message) {
	defer r.parseWorkers.Release()
	var (
		deathDate time.Time
	)
	rule, err := findRule(r.rules, request.Destination)
	if err != nil {
		logging.Info(r.logger).Log(logging.MessageKey(), "Could not get rule", logging.ErrorKey(), err, "destination", request.Destination)
	}
	deviceId, event, err := parseRequest(request, rule.storePayload, r.payloadMaxSize, r.metadataMaxSize)
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, parseFailReason).Add(1.0)
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to parse request", logging.ErrorKey(), err.Error())
		return
	}
	marshalledEvent, err := json.Marshal(event)
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, marshalFailReason).Add(1.0)
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to marshal event", logging.ErrorKey(), err.Error())
		return
	}

	encyptedData, err := r.encypter.EncryptMessage(marshalledEvent)
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, encryptFailReason).Add(1.0)
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to marshal event", logging.ErrorKey(), err.Error())
		return
	}

	birthDate := time.Unix(event.Time, 0)
	if rule.ttl == 0 {
		deathDate = birthDate.Add(r.defaultTTL)
	} else {
		deathDate = birthDate.Add(rule.ttl)
	}

	record := db.Record{
		DeviceID:  deviceId,
		BirthDate: birthDate.Unix(),
		DeathDate: deathDate.Unix(),
		Data:      encyptedData,
		Type:      db.UnmarshalEvent(rule.eventType),
	}

	if r.measures != nil {
		r.measures.InsertingQeue.Add(1.0)
	}
	r.insertQueue <- record
}

func parseRequest(req wrp.Message, storePayload bool, payloadMaxSize int, metadataMaxSize int) (string, db.Event, error) {
	var (
		eventInfo db.Event
		err       error
	)

	// get state and id from dest
	base, _ := path.Split(req.Destination)
	base, deviceId := path.Split(path.Base(base))
	if deviceId == "" {
		return "", db.Event{}, emperror.WrapWith(errEmptyID, "id check failed", "request destination", req.Destination, "full message", req)
	}
	deviceId = strings.ToLower(deviceId)

	// verify wrp is the right type
	msg := req
	switch msg.Type {
	case wrp.SimpleEventMessageType:

	default:
		return "", db.Event{}, emperror.WrapWith(errUnexpectedWRPType, "message type check failed", "type", msg.Type, "full message", msg)
	}

	// get timestamp from wrp payload
	payload := make(map[string]interface{})
	if msg.Payload != nil && len(msg.Payload) > 0 {
		err = json.Unmarshal(msg.Payload, &payload)
		if err != nil {
			return "", db.Event{}, emperror.WrapWith(err, "failed to unmarshal payload", "full message", req, "payload", msg.Payload)
		}
	}

	// parse the time from the payload
	timeString, ok := payload["ts"].(string)
	if !ok {
		return "", db.Event{}, emperror.WrapWith(errTimestampString, "failed to parse timestamp", "full message", req, "payload", payload)
	}
	parsedTime, err := time.Parse(time.RFC3339Nano, timeString)
	if err != nil {
		return "", db.Event{}, emperror.WrapWith(err, "failed to parse timestamp", "full message", req)
	}
	eventInfo.Time = parsedTime.Unix()

	// store the source, destination, partner ids, and transaction uuid if present
	eventInfo.Source = msg.Source
	eventInfo.Destination = msg.Destination
	eventInfo.PartnerIDs = msg.PartnerIDs
	eventInfo.TransactionUUID = msg.TransactionUUID

	// store the payload if we are supposed to and it's not too big
	if storePayload && len(msg.Payload) <= payloadMaxSize {
		eventInfo.Payload = msg.Payload
	}

	eventInfo.Details = make(map[string]interface{})

	// if metadata is too large, store a message explaining that instead of the metadata
	marshaledMetadata, err := json.Marshal(msg.Metadata)
	if err != nil {
		return "", db.Event{}, emperror.WrapWith(err, "failed to marshal metadata to determine size", "metadata", msg.Metadata, "full message", req)
	}
	if len(marshaledMetadata) > metadataMaxSize {
		eventInfo.Details["error"] = "metadata provided exceeds size limit - too big to store"
		return deviceId, eventInfo, nil
	}

	// store all metadata if all is good
	for key, val := range msg.Metadata {
		eventInfo.Details[key] = val
	}

	return deviceId, eventInfo, nil
}

func (r *RequestHandler) handleRecords() {
	var (
		records      []db.Record
		timeToSubmit time.Time
	)
	defer r.wg.Done()
	for record := range r.insertQueue {
		// if we don't have any records, then this is our first and started
		// the timer until submitting
		if len(records) == 0 {
			timeToSubmit = time.Now().Add(r.maxBatchWaitTime)
		}

		if r.measures != nil {
			r.measures.InsertingQeue.Add(-1.0)
		}
		records = append(records, record)

		// if we have filled up the batch or if we are out of time, we insert
		// what we have
		if len(records) >= r.maxBatchSize || time.Now().After(timeToSubmit) {
			r.insertWorkers.Acquire()
			go r.insertRecords(records)
			// don't need to remake an array each time, just remove the values
			records = records[:0]
		}

	}

	// Grab all the workers to make sure they are done.
	for i := 0; i < r.maxInsertWorkers; i++ {
		r.insertWorkers.Acquire()
	}
}

func (r *RequestHandler) insertRecords(records []db.Record) {
	defer r.insertWorkers.Release()
	err := r.inserter.InsertRecords(records...)
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, dbFailReason).Add(float64(len(records)))
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to add records to the database", logging.ErrorKey(), err.Error())
		return
	}
	logging.Debug(r.logger).Log(logging.MessageKey(), "Successfully upserted device information", "records", records)
	logging.Info(r.logger).Log(logging.MessageKey(), "Successfully upserted device information", "number of records", len(records))
}

type App struct {
	requestQueue chan wrp.Message
	logger       log.Logger
	token        string
	secretGetter secretGetter
	measures     *Measures
}

func (app *App) handleWebhook(writer http.ResponseWriter, req *http.Request) {
	var message wrp.Message
	msgBytes, err := ioutil.ReadAll(req.Body)
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not read request body", logging.ErrorKey(), err.Error())
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// verify this is valid from caduceus
	encodedSecret := req.Header.Get("X-Webpa-Signature")
	trimedSecret := strings.TrimPrefix(encodedSecret, "sha1=")
	secretGiven, err := hex.DecodeString(trimedSecret)
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not decode signature", logging.ErrorKey(), err.Error())
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	secret, err := app.secretGetter.GetSecret()
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not get secret", logging.ErrorKey(), err.Error())
		writer.WriteHeader(http.StatusInternalServerError)
	}
	h := hmac.New(sha1.New, []byte(secret))
	h.Write(msgBytes)
	sig := h.Sum(nil)
	if !hmac.Equal(sig, secretGiven) {
		logging.Error(app.logger).Log(logging.MessageKey(), "Invalid secret")
		writer.WriteHeader(http.StatusForbidden)
		return
	}

	// TODO: Update WRP library
	err = wrp.NewDecoderBytes(msgBytes, wrp.Msgpack).Decode(&message)
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not decode request body", logging.ErrorKey(), err.Error())
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	logging.Debug(app.logger).Log(logging.MessageKey(), "message info", "message type", message.Type, "full", message)
	app.requestQueue <- message
	if app.measures != nil {
		app.measures.ParsingQueue.Add(1.0)
	}
	writer.WriteHeader(http.StatusAccepted)
}
