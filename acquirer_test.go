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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/xmidt-org/bascule/acquire"
)

func TestDetermineTokenAcquirer(t *testing.T) {
	defaultAcquirer := &acquire.DefaultAcquirer{}
	goodBasicAcquirer, err := acquire.NewFixedAuthAcquirer("test basic")
	assert.Nil(t, err)
	options := acquire.RemoteBearerTokenAcquirerOptions{
		AuthURL: "/test",
		Timeout: 10 * time.Minute,
		Buffer:  5 * time.Second,
	}
	tests := []struct {
		description           string
		jwtConfig             acquire.RemoteBearerTokenAcquirerOptions
		basicVal              string
		expectJWT             bool
		expectedTokenAcquirer acquire.Acquirer
	}{
		{
			description: "Sat Success",
			jwtConfig:   options,
			expectJWT:   true,
		},
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
			tokenAcquirer, err := determineTokenAcquirer(config)
			assert.Nil(err)
			if tc.expectJWT {
				assert.NotEqual(goodBasicAcquirer, tokenAcquirer)
				assert.NotEqual(defaultAcquirer, tokenAcquirer)
			} else {
				assert.Equal(tc.expectedTokenAcquirer, tokenAcquirer)
			}
		})
	}
}
