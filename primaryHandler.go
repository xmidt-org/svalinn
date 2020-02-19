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
	"io/ioutil"
	"net/http"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/xmidt-org/svalinn/requestParser"
	"github.com/xmidt-org/webpa-common/logging"
	"github.com/xmidt-org/wrp-go/v2"
)

type parser interface {
	Parse(requestParser.WrpWithTime) error
}

type timeTracker interface {
	TrackTime(time.Duration)
}

type App struct {
	parser      parser
	logger      log.Logger
	timeTracker timeTracker
}

func (app *App) handleWebhook(writer http.ResponseWriter, req *http.Request) {
	begin := time.Now()
	var message wrp.Message
	msgBytes, err := ioutil.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not read request body", logging.ErrorKey(), err.Error())
		writer.WriteHeader(http.StatusBadRequest)
		app.timeTracker.TrackTime(time.Now().Sub(begin))
		return
	}

	err = wrp.NewDecoderBytes(msgBytes, wrp.Msgpack).Decode(&message)
	if err != nil {
		logging.Error(app.logger).Log(logging.MessageKey(), "Could not decode request body", logging.ErrorKey(), err.Error())
		writer.WriteHeader(http.StatusBadRequest)
		app.timeTracker.TrackTime(time.Now().Sub(begin))
		return
	}

	logging.Debug(app.logger).Log(logging.MessageKey(), "message info", "messageType", message.Type, "fullMsg", message)
	err = app.parser.Parse(requestParser.WrpWithTime{Message: message, Beginning: begin})
	if err != nil {
		logging.Warn(app.logger).Log(logging.ErrorKey(), err.Error())
		// end := time.Now()
		// todo: send beginning and end times to metric handler
		writer.WriteHeader(http.StatusTooManyRequests)
		app.timeTracker.TrackTime(time.Now().Sub(begin))
		return
	}
	writer.WriteHeader(http.StatusAccepted)
}
