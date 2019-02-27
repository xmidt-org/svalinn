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
	"time"

	"github.com/Comcast/codex/db"

	"github.com/stretchr/testify/assert"

	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
)

func TestParseRequest(t *testing.T) {
	testassert := assert.New(t)
	goodTime, err := time.Parse(time.RFC3339Nano, "2019-02-13T21:19:02.614191735Z")
	testassert.Nil(err)
	goodEvent := db.Event{
		Time:            goodTime.Unix(),
		Source:          "test source",
		Destination:     "/test/",
		PartnerIDs:      []string{"test1", "test2"},
		TransactionUUID: "transaction test uuid",
		Payload:         []byte(`{"ts":"2019-02-13T21:19:02.614191735Z"}`),
		Details:         map[string]interface{}{"testkey": "testvalue"},
	}
	goodEventNoMetadata := db.Event{
		Time:        goodTime.Unix(),
		Destination: "/test/",
		Details:     map[string]interface{}{"error": "metadata provided exceeds size limit - too big to store"},
	}
	tests := []struct {
		description      string
		req              wrp.Message
		storePayload     bool
		maxPayloadSize   int
		maxMetadataSize  int
		expectedDeviceID string
		expectedEvent    db.Event
		expectedErr      error
	}{
		{
			description: "Success",
			req: wrp.Message{
				Source:          goodEvent.Source,
				Destination:     goodEvent.Destination,
				PartnerIDs:      goodEvent.PartnerIDs,
				TransactionUUID: goodEvent.TransactionUUID,
				Type:            wrp.SimpleEventMessageType,
				Payload:         []byte(`{"ts":"2019-02-13T21:19:02.614191735Z"}`),
				Metadata:        map[string]string{"testkey": "testvalue"},
			},
			expectedDeviceID: "test",
			expectedEvent:    goodEvent,
			storePayload:     true,
			maxMetadataSize:  500,
			maxPayloadSize:   500,
		},
		{
			description: "Success Empty Metadata",
			req: wrp.Message{
				Source:          goodEventNoMetadata.Source,
				Destination:     goodEventNoMetadata.Destination,
				PartnerIDs:      goodEventNoMetadata.PartnerIDs,
				TransactionUUID: goodEventNoMetadata.TransactionUUID,
				Type:            wrp.SimpleEventMessageType,
				Payload:         []byte(`{"ts":"2019-02-13T21:19:02.614191735Z"}`),
				Metadata:        map[string]string{"testkey": "testvalue"},
			},
			expectedDeviceID: "test",
			expectedEvent:    goodEventNoMetadata,
		},
		{
			description: "Empty ID Error",
			req: wrp.Message{
				Destination: "//",
			},
			expectedErr: errEmptyID,
		},
		{
			description: "Unexpected WRP Type Error",
			req: wrp.Message{
				Destination: "/device/",
				Type:        5,
			},
			expectedErr: errUnexpectedWRPType,
		},
		{
			description: "Unmarshal Payload Error",
			req: wrp.Message{
				Destination: "/device/",
				Type:        wrp.SimpleEventMessageType,
				Payload:     []byte("test"),
			},
			expectedErr: errors.New("failed to unmarshal payload"),
		},
		{
			description: "Parse Timestamp Error",
			req: wrp.Message{
				Destination: "/device/",
				Type:        wrp.SimpleEventMessageType,
				Payload:     []byte(`{"ts":"2345"}`),
			},
			expectedErr: errors.New("failed to parse timestamp"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			id, event, err := parseRequest(tc.req, tc.storePayload, tc.maxPayloadSize, tc.maxMetadataSize)
			assert.Equal(tc.expectedDeviceID, id)
			assert.Equal(tc.expectedEvent, event)
			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}
		})
	}
}

func TestHandleWebhook(t *testing.T) {
	secret := "abcdefgh"
	goodMsg := wrp.Message{
		Type:        wrp.SimpleEventMessageType,
		Source:      "test",
		Destination: "test",
	}

	tests := []struct {
		description        string
		requestBody        interface{}
		includeSignature   bool
		getSecretCalled    bool
		secret             string
		getSecretErr       error
		expectedHeader     int
		expectingMsg       bool
		expectedMsgOnQueue wrp.Message
	}{
		{
			description:        "Success",
			requestBody:        goodMsg,
			includeSignature:   true,
			getSecretCalled:    true,
			expectedHeader:     http.StatusAccepted,
			expectingMsg:       true,
			expectedMsgOnQueue: goodMsg,
		},
		{
			description:      "Decode Body Error",
			requestBody:      "{{{{{{{{{",
			includeSignature: true,
			expectedHeader:   http.StatusBadRequest,
		},
		{
			description:        "Get Secret Failure",
			requestBody:        goodMsg,
			getSecretCalled:    true,
			getSecretErr:       errors.New("get secret test error"),
			expectedHeader:     http.StatusInternalServerError,
			expectedMsgOnQueue: goodMsg,
		},
		{
			description:        "Mismatched Secret Error",
			requestBody:        goodMsg,
			getSecretCalled:    true,
			expectedHeader:     http.StatusForbidden,
			expectedMsgOnQueue: goodMsg,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			queue := make(chan wrp.Message, 2)
			mockSecretGetter := new(mockSecretGetter)
			if tc.getSecretCalled {
				mockSecretGetter.On("GetSecret").Return(secret, tc.getSecretErr).Once()
			}
			app := &App{
				requestQueue: queue,
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
				h.Write(tc.expectedMsgOnQueue.Payload)
				sig := fmt.Sprintf("sha1=%s", hex.EncodeToString(h.Sum(nil)))
				request.Header.Set("X-Webpa-Signature", sig)
			}

			app.handleWebhook(rr, request)
			mockSecretGetter.AssertExpectations(t)
			assert.Equal(tc.expectedHeader, rr.Code)
			if tc.expectingMsg {
				select {
				case msg := <-queue:
					assert.Equal(tc.expectedMsgOnQueue, msg)
				default:
					assert.Fail("expected a message to be on the queue", "expected message", tc.expectedMsgOnQueue)
				}
			}
		})
	}
}