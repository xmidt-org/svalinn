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
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
)

type RequestHandler struct {
	inserter            db.RetryInsertService
	updater             db.RetryUpdateService
	getter              db.RetryEGService
	logger              log.Logger
	tombstoneRules      []rule
	metadataMaxSize     int
	payloadMaxSize      int
	stateLimitPerDevice int
	defaultTTL          time.Duration
	pruneQueue          chan string
}

func (r *RequestHandler) handleRequests(requestQueue chan wrp.Message) {
	for request := range requestQueue {
		// TODO: need to add limit to goroutines
		go r.handleRequest(request)
	}
}

func (r *RequestHandler) handlePruning() {
	for _ = range r.pruneQueue {
		go r.pruneDevice()
	}
}

func (r *RequestHandler) pruneDevice() {
	err := r.updater.PruneRecords(time.Now())
	if err != nil {
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to update event history", logging.ErrorKey(), err.Error())
		return
	}
	logging.Info(r.logger).Log(logging.MessageKey(), "Successfully pruned events")
	return
}

func (r *RequestHandler) handleRequest(request wrp.Message) {
	var (
		deathDate time.Time
	)
	rule, err := findRule(r.tombstoneRules, request.Destination)
	if err != nil {
		logging.Info(r.logger).Log(logging.MessageKey(), "Could not get key for tombstone", logging.ErrorKey(), err, "destination", request.Destination)
	}
	deviceId, event, err := parseRequest(request, rule.storePayload, r.payloadMaxSize, r.metadataMaxSize)
	if err != nil {
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to parse request", logging.ErrorKey(), err.Error())
		return
	}
	marshalledEvent, err := json.Marshal(event)
	if err != nil {
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to marshal event", logging.ErrorKey(), err.Error())
	}

	birthDate := time.Unix(event.Time, 0)
	if rule.ttl == 0 {
		deathDate = birthDate.Add(r.defaultTTL)
	} else {
		deathDate = birthDate.Add(rule.ttl)
	}

	record := db.Record{
		DeviceID:  deviceId,
		BirthDate: birthDate,
		DeathDate: deathDate,
		Data:      marshalledEvent,
		Type:      db.UnmarshalEvent(rule.eventType),
	}

	err = r.inserter.InsertRecord(record)
	if err != nil {
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to add state information to the database", logging.ErrorKey(), err.Error())
		return
	}
	logging.Info(r.logger).Log(logging.MessageKey(), "Successfully upserted device information", "device", deviceId, "event", event, "record", record)
	r.pruneQueue <- deviceId
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
		return "", db.Event{}, emperror.WrapWith(errors.New("Empty id is invalid"), "id check failed", "request destination", req.Destination, "full message", req)
	}

	// verify wrp is the right type
	msg := req
	switch msg.Type {
	case wrp.SimpleEventMessageType:

	default:
		return "", db.Event{}, emperror.WrapWith(errors.New("Unexpected wrp message type"), "message type check failed", "type", msg.Type, "full message", msg)
	}

	// get timestamp from wrp payload
	payload := make(map[string]interface{})
	if msg.Payload != nil && len(msg.Payload) > 0 {
		err = json.Unmarshal(msg.Payload, &payload)
		if err != nil {
			return "", db.Event{}, emperror.WrapWith(err, "failed to unmarshal payload", "payload", msg.Payload)
		}
	}

	// parse the time from the payload
	timeString := payload["ts"].(string)
	parsedTime, err := time.Parse(time.RFC3339Nano, timeString)
	if err != nil {
		return "", db.Event{}, err
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
		return "", db.Event{}, emperror.WrapWith(err, "failed to marshal metadata to determine size", "metadata", msg.Metadata)
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

type App struct {
	requestQueue chan wrp.Message
	logger       log.Logger
	token        string
	secret       string
}

func (app *App) handleWebhook(writer http.ResponseWriter, req *http.Request) {
	var message wrp.Message
	msgBytes, err := ioutil.ReadAll(req.Body)
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not read request body", logging.ErrorKey(), err.Error())
		writer.WriteHeader(400)
		return
	}

	// verify this is valid from caduceus
	encodedSecret := req.Header.Get("X-Webpa-Signature")
	secret, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encodedSecret, "sha1="))
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not decode signature", logging.ErrorKey(), err.Error())
		writer.WriteHeader(400)
		return
	}

	h := hmac.New(sha1.New, []byte(app.secret))
	h.Write(msgBytes)
	sig := h.Sum(nil)
	if !hmac.Equal(sig, secret) {
		logging.Error(app.logger).Log(logging.MessageKey(), "Invalid secret")
		writer.WriteHeader(403)
		return
	}

	// TODO: Update WRP library
	err = wrp.NewDecoderBytes(msgBytes, wrp.Msgpack).Decode(&message)
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not decode request body", logging.ErrorKey(), err.Error())
		writer.WriteHeader(400)
		return
	}
	logging.Debug(app.logger).Log(logging.MessageKey(), "message info", "message type", message.Type, "full", message)
	app.requestQueue <- message
	writer.WriteHeader(202)
}
