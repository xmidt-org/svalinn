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
	"encoding/json"
	"errors"
	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
	"io/ioutil"
	"net/http"
	"path"
	"regexp"
	"time"
)

func handleRequests(requestQueue chan wrp.Message, pruneQueue chan string, logger log.Logger, dbConn db.Connection, tombstoneRules map[string]*regexp.Regexp) {
	for request := range requestQueue {
		// TODO: need to add semaphore/limit to goroutines
		go handleRequest(request, pruneQueue, logger, dbConn, tombstoneRules)
	}
}

func handlePruning(pruneQueue chan string, logger log.Logger, dbConn db.Connection, limit int) {
	for device := range pruneQueue {
		go pruneDevice(device, logger, dbConn, limit)
	}
}

func pruneDevice(deviceId string, logger log.Logger, dbConn db.Connection, limit int) {

	history, err := dbConn.GetHistory(deviceId)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to get device", logging.ErrorKey(), err.Error())
		return
	}

	numEvents := len(history.Events)
	if numEvents > limit {
		newEventList := history.Events[:limit]
		err = dbConn.UpdateHistory(deviceId, newEventList)
		if err != nil {
			logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(),
				"Failed to update event history", logging.ErrorKey(), err.Error(), "device", deviceId, "limit", limit)
			return
		}
		logging.Info(logger).Log(logging.MessageKey(), "Successfully pruned event history", "device", deviceId, "limit", limit)
		return
	}
	logging.Info(logger).Log(logging.MessageKey(), "No need to prune event history", "device", deviceId, "limit", limit,
		"number of states", numEvents)
}

func handleRequest(request wrp.Message, pruneQueue chan string, logger log.Logger, dbConn db.Connection, tombstoneRules map[string]*regexp.Regexp) {
	deviceId, event, err := parseRequest(request)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to parse request", logging.ErrorKey(), err.Error())
		return
	}
	key, err := getTombstoneKey(tombstoneRules, event.Destination)
	if err != nil {
		logging.Info(logger).Log(logging.MessageKey(), "Could not get key for tombstone", logging.ErrorKey(), err, "destination", event.Destination)
	}
	err = dbConn.InsertEvent(deviceId, event, key)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to add state information to the database", logging.ErrorKey(), err.Error())
		return
	}
	logging.Info(logger).Log(logging.MessageKey(), "Successfully upserted device information", "device", deviceId, "events", event)
	pruneQueue <- deviceId
}

func parseRequest(req wrp.Message) (string, db.Event, error) {
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

	eventInfo.Details = make(map[string]interface{})

	// store all metadata
	for key, val := range msg.Metadata {
		eventInfo.Details[key] = val
	}

	return deviceId, eventInfo, nil
}

type App struct {
	requestQueue chan wrp.Message
	logger       log.Logger
}

func (app *App) handleWebhook(writer http.ResponseWriter, req *http.Request) {
	var message wrp.Message
	msgBytes, err := ioutil.ReadAll(req.Body)
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not read request body", logging.ErrorKey(), err.Error())
		writer.WriteHeader(400)
		return
	}
	err = wrp.NewDecoderBytes(msgBytes, wrp.JSON).Decode(&message)
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not decode request body", logging.ErrorKey(), err.Error())
		writer.WriteHeader(400)
		return
	}
	logging.Info(app.logger).Log(logging.MessageKey(), "message info", "message type", message.Type, "full", message)
	app.requestQueue <- message
	writer.WriteHeader(200)
}
