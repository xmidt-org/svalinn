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
	"github.com/xmidt-org/bascule/acquire"
)

// determineTokenAcquirer always returns a valid TokenAcquirer, but may also return an error
func determineTokenAcquirer(config WebhookConfig) acquire.Acquirer {
	defaultAcquirer := &acquire.DefaultAcquirer{}
	if config.JWT.Client != "" && config.JWT.URL != "" && config.JWT.Secret != "" && config.JWT.Timeout != 0 {
		acquirer := acquire.NewJWTAcquirer(acquire.JWTAcquirerOptions{
			AuthURL:        config.JWT.URL,
			Timeout:        config.JWT.Timeout,
			Buffer:         config.JWT.Buffer,
			RequestHeaders: map[string]string{"X-Client-Id": config.JWT.Client, "X-Client-Secret": config.JWT.Secret},
		})
		return &acquirer
	}

	if config.Basic != "" {
		return acquire.NewBasicAcquirer(config.Basic)
	}

	return defaultAcquirer
}
