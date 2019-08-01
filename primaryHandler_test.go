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
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/xmidt-org/webpa-common/logging"
	"github.com/xmidt-org/wrp-go/wrp"
)

func TestHandleWebhook(t *testing.T) {
	goodMsg := wrp.Message{
		Type:        wrp.SimpleEventMessageType,
		Source:      "test",
		Destination: "test",
	}

	tests := []struct {
		description    string
		requestBody    interface{}
		expectedHeader int
		parseCalled    bool
		parseErr       error
	}{
		{
			description:    "Success",
			requestBody:    goodMsg,
			expectedHeader: http.StatusAccepted,
			parseCalled:    true,
		},
		{
			description:    "Decode Body Error",
			requestBody:    "{{{{{{{{{",
			expectedHeader: http.StatusBadRequest,
		},
		{
			description:    "Parse Error",
			requestBody:    goodMsg,
			expectedHeader: http.StatusTooManyRequests,
			parseCalled:    true,
			parseErr:       errors.New("test parse error"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			mockParser := new(mockParser)
			if tc.parseCalled {
				mockParser.On("Parse", mock.Anything).Return(tc.parseErr).Once()
			}

			app := &App{
				parser: mockParser,
				logger: logging.DefaultLogger(),
			}
			rr := httptest.NewRecorder()
			var marshaledMsg []byte
			var err error
			if tc.requestBody != nil {
				err = wrp.NewEncoderBytes(&marshaledMsg, wrp.Msgpack).Encode(tc.requestBody)
				assert.Nil(err)
			}
			assert.NotNil(marshaledMsg)
			request, err := http.NewRequest(http.MethodGet, "/", bytes.NewReader(marshaledMsg))
			assert.Nil(err)

			app.handleWebhook(rr, request)
			mockParser.AssertExpectations(t)
			assert.Equal(tc.expectedHeader, rr.Code)
		})
	}
}
