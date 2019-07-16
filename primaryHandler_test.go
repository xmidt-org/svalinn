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
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/xmidt-org/webpa-common/logging"
	"github.com/xmidt-org/wrp-go/wrp"
)

func TestHandleWebhook(t *testing.T) {
	secret := "abcdefgh"
	goodMsg := wrp.Message{
		Type:        wrp.SimpleEventMessageType,
		Source:      "test",
		Destination: "test",
	}

	tests := []struct {
		description      string
		requestBody      interface{}
		includeSignature bool
		getSecretCalled  bool
		secret           string
		getSecretErr     error
		expectedHeader   int
		parseCalled      bool
		parseErr         error
	}{
		{
			description:      "Success",
			requestBody:      goodMsg,
			includeSignature: true,
			getSecretCalled:  true,
			expectedHeader:   http.StatusAccepted,
			parseCalled:      true,
		},
		{
			description:      "Decode Body Error",
			requestBody:      "{{{{{{{{{",
			includeSignature: true,
			getSecretCalled:  true,
			expectedHeader:   http.StatusBadRequest,
		},
		{
			description:     "Get Secret Failure",
			requestBody:     goodMsg,
			getSecretCalled: true,
			getSecretErr:    errors.New("get secret test error"),
			expectedHeader:  http.StatusInternalServerError,
		},
		{
			description:     "Mismatched Secret Error",
			requestBody:     goodMsg,
			getSecretCalled: true,
			expectedHeader:  http.StatusForbidden,
		},
		{
			description:      "Parse Error",
			requestBody:      goodMsg,
			includeSignature: true,
			getSecretCalled:  true,
			expectedHeader:   http.StatusTooManyRequests,
			parseCalled:      true,
			parseErr:         errors.New("test parse error"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			mockSecretGetter := new(mockSecretGetter)
			if tc.getSecretCalled {
				mockSecretGetter.On("GetSecret").Return(secret, tc.getSecretErr).Once()
			}
			mockParser := new(mockParser)
			if tc.parseCalled {
				mockParser.On("Parse", mock.Anything).Return(tc.parseErr).Once()
			}

			app := &App{
				parser:       mockParser,
				secretGetter: mockSecretGetter,
				logger:       logging.DefaultLogger(),
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
			if tc.includeSignature {
				h := hmac.New(sha1.New, []byte(secret))
				if tc.secret != "" {
					h = hmac.New(sha1.New, []byte(tc.secret))
				}
				h.Write(marshaledMsg)
				sig := fmt.Sprintf("sha1=%s", hex.EncodeToString(h.Sum(nil)))
				request.Header.Set("X-Webpa-Signature", sig)
			}

			app.handleWebhook(rr, request)
			mockSecretGetter.AssertExpectations(t)
			mockParser.AssertExpectations(t)
			assert.Equal(tc.expectedHeader, rr.Code)
		})
	}
}
