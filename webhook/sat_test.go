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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSatAcquireSuccess(t *testing.T) {
	goodSat := SatToken{
		Token: "test token",
	}
	goodToken := "Bearer test token"

	tests := []struct {
		description        string
		satToken           interface{}
		satURL             string
		returnUnauthorized bool
		expectedToken      string
		expectedErr        error
	}{
		{
			description:   "Success",
			satToken:      goodSat,
			expectedToken: goodToken,
			expectedErr:   nil,
		},
		{
			description:   "HTTP Make Request Error",
			satToken:      goodSat,
			expectedToken: "",
			satURL:        "/\b",
			expectedErr:   errors.New("failed to create new request for SAT"),
		},
		{
			description:   "HTTP Do Error",
			satToken:      goodSat,
			expectedToken: "",
			satURL:        "/",
			expectedErr:   errors.New("error acquiring SAT token"),
		},
		{
			description:        "HTTP Unauthorized Error",
			satToken:           goodSat,
			returnUnauthorized: true,
			expectedToken:      "",
			expectedErr:        errors.New("received non 200 code"),
		},
		{
			description:   "Unmarshal Error",
			satToken:      []byte("{token:5555}"),
			expectedToken: "",
			expectedErr:   errors.New("unable to read json"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)

			// Start a local HTTP server
			server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				// Send response to be tested
				if tc.returnUnauthorized {
					rw.WriteHeader(http.StatusUnauthorized)
					return
				}
				marshaledSat, err := json.Marshal(tc.satToken)
				assert.Nil(err)
				rw.Write(marshaledSat)
			}))
			// Close the server when test finishes
			defer server.Close()

			url := server.URL
			if tc.satURL != "" {
				url = tc.satURL
			}

			// Use Client & URL from our local test server
			sat := SatAcquirer{
				SatURL:  url,
				Timeout: time.Duration(5) * time.Second,
			}
			token, err := sat.Acquire()

			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}
			assert.Equal(tc.expectedToken, token)
		})
	}
}
