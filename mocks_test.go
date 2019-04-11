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

type mockEncrypter struct {
	mock.Mock
}

func (md *mockEncrypter) EncryptMessage(message []byte) ([]byte, error) {
	args := md.Called(message)
	return message, args.Error(0)
}

type mockBlacklist struct {
	mock.Mock
}

func (mb *mockBlacklist) InList(ID string) (reason string, ok bool) {
	args := mb.Called(ID)
	reason = args.String(0)
	ok = args.Bool(1)
	return
}