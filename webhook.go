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
	"encoding/json"
	"fmt"
	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

var RequestAuthorization string

type Webhook struct {
	Secret               string        `json:",secret"`
	RegistrationInterval time.Duration `json:",registrationInterval"`
	URL                  string        `json:",url"`
	Timeout              time.Duration `json:",timeout"`
	RegistrationURL      string        `json:",registrationURL"`

	Logger log.Logger
}

type WebhookConfig struct {
	// The URL to deliver messages to.
	URL string `json:"url"`

	// The content-type to set the messages to (unless specified by WRP).
	ContentType string `json:"content_type"`

	// The secret to use for the SHA1 HMAC.
	// Optional, set to "" to disable behavior.
	Secret string `json:"secret,omitempty"`
}
type WebhookMatcher struct {
	// The list of regular expressions to match device id type against.
	DeviceId []string `json:"device_id"`
}

type WebhookBody struct {
	Config WebhookConfig `json:"config"`
	// Matcher type contains values to match against the metadata.
	Matcher WebhookMatcher `json:"matcher,omitempty"`
	Events  []string       `json:"events"`
}

func stringInList(key string, list []string) bool {
	for _, b := range list {
		if strings.Compare(key, b) == 0 {
			return true
		}
	}
	return false
}

func AcquireSatToken(client string, secret string, satURL string) (string, error) {
	type satToken struct {
		Expiration float64 `json:"expires_in"`
		Token      string  `json:"serviceAccessToken"`
	}
	jsonStr := []byte(`{}`) //TODO: in case we need to specify something in the future
	httpclient := &http.Client{}
	req, _ := http.NewRequest("GET", satURL, bytes.NewBuffer(jsonStr))
	req.Header.Set("X-Client-Id", client)
	req.Header.Set("X-Client-Secret", secret)

	resp, errHTTP := httpclient.Do(req)
	if errHTTP != nil {

		return "", fmt.Errorf("error acquiring SAT token: [%s]", errHTTP.Error())
	}
	defer resp.Body.Close()

	respBody, errRead := ioutil.ReadAll(resp.Body)
	if errRead != nil {

		return "", fmt.Errorf("error reading SAT token: [%s]", errRead.Error())
	}

	var sat satToken

	if errUnmarshal := json.Unmarshal(respBody, &sat); errUnmarshal != nil {
		return "", fmt.Errorf("unable to read json in SAT response: [%s]", errUnmarshal.Error())
	}
	return fmt.Sprintf("Bearer %s", sat.Token), nil
}

func (webhook *Webhook) Register() error {

	tempEvents := []string{".*"}
	tempMacs := []string{".*"}

	//create webhookbody
	webhookBody := WebhookBody{
		Config:  WebhookConfig{URL: webhook.URL, ContentType: "wrp", Secret: webhook.Secret},
		Matcher: WebhookMatcher{DeviceId: tempMacs},
		Events:  tempEvents,
	}

	jsonStr, errMarshal := json.Marshal(&webhookBody)
	if errMarshal != nil {
		return emperror.WrapWith(errMarshal, "failed to marshal")
	}

	req, _ := http.NewRequest("POST", webhook.RegistrationURL, bytes.NewBuffer(jsonStr))
	req.Header.Set("Authorization", webhook.Secret)
	httpclient := &http.Client{
		Timeout: webhook.Timeout,
	}

	resp, err := httpclient.Do(req)
	if err != nil {
		return emperror.WrapWith(err, "failed to make http request")
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return emperror.WrapWith(err, "failed to read body")
	} else {
		if resp.StatusCode != 200 {
			return emperror.WrapWith(fmt.Errorf("unable to register webhook"), "received non-200 response", "code", resp.StatusCode, "body", string(respBody[:]))
		}
	}
	return nil
}
