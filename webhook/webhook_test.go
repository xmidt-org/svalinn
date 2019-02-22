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

package webhook

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/stretchr/testify/assert"
)

func TestDefaultAcquirer(t *testing.T) {
	assert := assert.New(t)
	acquirer := DefaultAcquirer{}
	val, err := acquirer.Acquire()
	assert.Nil(err)
	assert.Equal("", val)
}

func TestWebhookRegister(t *testing.T) {
	goodResponse := &http.Response{
		StatusCode: http.StatusOK,
		Body:       ioutil.NopCloser(bytes.NewBufferString(`OK`)),
		Header:     make(http.Header),
	}
	badResponse := &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       ioutil.NopCloser(bytes.NewBufferString(`OK`)),
		Header:     make(http.Header),
	}

	acquireErr := errors.New("test acquire error")
	requestErr := errors.New("test request response error")

	tests := []struct {
		description        string
		acquireCalled      bool
		acquireErr         error
		request            bool
		requestResponse    *http.Response
		requestResponseErr error
		expectedErr        error
	}{
		{
			description:     "Success",
			acquireCalled:   true,
			request:         true,
			requestResponse: goodResponse,
			expectedErr:     nil,
		},
		{
			description:   "Acquire Error",
			acquireCalled: true,
			acquireErr:    acquireErr,
			expectedErr:   acquireErr,
		},
		{
			description:        "Do Error",
			acquireCalled:      true,
			request:            true,
			requestResponse:    goodResponse,
			requestResponseErr: requestErr,
			expectedErr:        requestErr,
		},
		{
			description:     "Bad Response Error",
			acquireCalled:   true,
			request:         true,
			requestResponse: badResponse,
			expectedErr:     errors.New("received non-200 response"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			mockTransport := new(MockRoundTripper)
			if tc.request {
				mockTransport.On("RoundTrip", mock.Anything, mock.Anything).Return(tc.requestResponse, tc.requestResponseErr).Once()
			}
			client := &http.Client{
				Transport: mockTransport,
			}
			mockAcquirer := new(MockAcquirer)
			if tc.acquireCalled {
				mockAcquirer.On("Acquire").Return("testtoken", tc.acquireErr).Once()
			}
			wh := &Webhook{
				Acquirer: mockAcquirer,
			}
			err := wh.Register(client, "")
			mockTransport.AssertExpectations(t)
			mockAcquirer.AssertExpectations(t)
			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}
		})
	}
}
