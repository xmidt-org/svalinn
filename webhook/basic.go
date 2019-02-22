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

import "errors"

var (
	errMissingCredentials = errors.New("no credentials found")
)

type BasicAcquirer struct {
	encodedCredentials string
}

func (b *BasicAcquirer) Acquire() (string, error) {
	if b.encodedCredentials == "" {
		return "", errMissingCredentials
	}
	return "Basic " + b.encodedCredentials, nil
}

func NewBasicAcquirer(credentials string) *BasicAcquirer {
	return &BasicAcquirer{credentials}
}
