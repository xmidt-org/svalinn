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
	"net/http"

	"github.com/xmidt-org/wrp-go/wrp"
	"github.com/stretchr/testify/mock"
)

type mockRegisterer struct {
	mock.Mock
}

func (r *mockRegisterer) Register(client *http.Client, secret string) error {
	args := r.Called(client, secret)
	return args.Error(0)
}

type mockSecretGetter struct {
	mock.Mock
}

func (sg *mockSecretGetter) GetSecret() (string, error) {
	args := sg.Called()
	return args.String(0), args.Error(1)
}

type mockParser struct {
	mock.Mock
}

func (p *mockParser) Parse(message wrp.Message) error {
	args := p.Called(message)
	return args.Error(0)
}
