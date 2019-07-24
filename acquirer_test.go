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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xmidt-org/bascule/acquire"
)

func TestDetermineTokenAcquirer(t *testing.T) {
	defaultAcquirer := &acquire.DefaultAcquirer{}
	goodBasicAcquirer := acquire.NewBasicAcquirer("test basic")
	// config := JWTConfig{
	// 	Client:  "test client",
	// 	Secret:  "test secret",
	// 	URL:     "/test",
	// 	Buffer:  5 * time.Second,
	// 	Timeout: time.Duration(5) * time.Second,
	// }
	// goodJWTAcquirer := acquire.NewJWTAcquirer(acquire.JWTAcquirerOptions{
	// 	AuthURL:        config.URL,
	// 	Timeout:        config.Timeout,
	// 	Buffer:         config.Buffer,
	// 	RequestHeaders: map[string]string{"X-Client-Id": config.Client, "X-Client-Secret": config.Secret},
	// })
	tests := []struct {
		description           string
		jwtConfig             JWTConfig
		basicVal              string
		expectedTokenAcquirer acquire.Acquirer
	}{
		// TODO: fix test
		// {
		// 	description:           "Sat Success",
		// 	jwtConfig:             config,
		// 	expectedTokenAcquirer: &goodJWTAcquirer,
		// },
		{
			description:           "Basic Success",
			basicVal:              "test basic",
			expectedTokenAcquirer: goodBasicAcquirer,
		},
		{
			description:           "Default Success",
			expectedTokenAcquirer: defaultAcquirer,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			config := WebhookConfig{
				JWT:   tc.jwtConfig,
				Basic: tc.basicVal,
			}
			tokenAcquirer := determineTokenAcquirer(config)
			assert.EqualValues(tc.expectedTokenAcquirer, tokenAcquirer)
		})
	}
}
