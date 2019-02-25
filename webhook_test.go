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
	"errors"
	"testing"
	"time"

	"github.com/Comcast/codex-svalinn/webhook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestGetSecretAndRegister(t *testing.T) {
	timeout := time.Duration(5) * time.Second
	getSecretErr := errors.New("get secret test error")
	registerErr := errors.New("register test error")

	tests := []struct {
		description    string
		getSecretErr   error
		registerCalled bool
		registerErr    error
		expectedErr    error
	}{
		{
			description:    "Success",
			registerCalled: true,
			expectedErr:    nil,
		},
		{
			description:  "Get Secret Error",
			getSecretErr: getSecretErr,
			expectedErr:  getSecretErr,
		},
		{
			description:    "Register Error",
			registerCalled: true,
			registerErr:    registerErr,
			expectedErr:    registerErr,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			mockSecretGetter := new(mockSecretGetter)
			mockSecretGetter.On("GetSecret").Return("test secret", tc.getSecretErr)
			mockRegisterer := new(mockRegisterer)
			if tc.registerCalled {
				mockRegisterer.On("Register", mock.Anything, "test secret").Return(tc.registerErr)
			}
			err := getSecretAndRegister(mockRegisterer, mockSecretGetter, timeout)
			mockSecretGetter.AssertExpectations(t)
			mockRegisterer.AssertExpectations(t)
			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}

		})
	}
}

func TestDetermineTokenAcquirer(t *testing.T) {
	defaultAcquirer := &webhook.DefaultAcquirer{}
	goodBasicAcquirer := webhook.NewBasicAcquirer("test basic")
	goodSatAcquirer := webhook.SatAcquirer{
		Client:  "test client",
		Secret:  "test secret",
		SatURL:  "/test",
		Timeout: time.Duration(5) * time.Second,
	}
	tests := []struct {
		description           string
		satVal                interface{}
		basicVal              interface{}
		expectedTokenAcquirer webhook.TokenAcquirer
		expectedErr           error
	}{
		{
			description:           "Sat Success",
			satVal:                goodSatAcquirer,
			expectedTokenAcquirer: &goodSatAcquirer,
		},
		{
			description:           "Sat Error",
			satVal:                "",
			expectedTokenAcquirer: defaultAcquirer,
			expectedErr:           errParseSat,
		},
		{
			description:           "Basic Success",
			basicVal:              "test basic",
			expectedTokenAcquirer: goodBasicAcquirer,
		},
		{
			description:           "Basic Parse Error",
			basicVal:              goodSatAcquirer,
			expectedTokenAcquirer: defaultAcquirer,
			expectedErr:           errParseBasic,
		},
		{
			description:           "Basic Empty Creds Error",
			basicVal:              "",
			expectedTokenAcquirer: defaultAcquirer,
			expectedErr:           errEmptyBasic,
		},
		{
			description:           "Default Success",
			expectedTokenAcquirer: defaultAcquirer,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			config := make(map[string]interface{})
			if tc.satVal != nil {
				config["sat"] = tc.satVal
			}
			if tc.basicVal != nil {
				config["basic"] = tc.basicVal
			}
			tokenAcquirer, err := determineTokenAcquirer(config)
			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}
			assert.Equal(tc.expectedTokenAcquirer, tokenAcquirer)
		})
	}
}
