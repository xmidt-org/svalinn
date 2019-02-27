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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/goph/emperror"
)

const wrpContentType = "wrp"

var RequestAuthorization string

type Webhook struct {
	URL             string   `json:",url"`
	RegistrationURL string   `json:",registrationURL"`
	EventsToWatch   []string `json:",eventsToWatch"`

	Acquirer TokenAcquirer
}

type webhookRequest struct {
	Config requestConfig `json:"config"`
	// Matcher type contains values to match against the metadata.
	Matcher requestMatcher `json:"matcher,omitempty"`
	Events  []string       `json:"events"`
}

type requestConfig struct {
	// The URL to deliver messages to.
	URL string `json:"url"`

	// The content-type to set the messages to (unless specified by WRP).
	ContentType string `json:"content_type"`

	// The secret to use for the SHA1 HMAC.
	// Optional, set to "" to disable behavior.
	Secret string `json:"secret,omitempty"`
}
type requestMatcher struct {
	// The list of regular expressions to match device id type against.
	DeviceId []string `json:"device_id"`
}

type TokenAcquirer interface {
	Acquire() (string, error)
}

type DefaultAcquirer struct{}

func (d *DefaultAcquirer) Acquire() (string, error) {
	return "", nil
}

func (webhook *Webhook) Register(client *http.Client, secret string) error {

	tempMacs := []string{".*"}

	//create webhook request body
	webhookBody := webhookRequest{
		Config: requestConfig{
			URL:         webhook.URL,
			ContentType: wrpContentType,
			Secret:      secret,
		},
		Matcher: requestMatcher{tempMacs},
		Events:  webhook.EventsToWatch,
	}

	marshaledBody, errMarshal := json.Marshal(&webhookBody)
	if errMarshal != nil {
		return emperror.WrapWith(errMarshal, "failed to marshal")
	}

	satToken, err := webhook.Acquirer.Acquire()
	if err != nil {
		return err
	}

	req, _ := http.NewRequest("POST", webhook.RegistrationURL, bytes.NewBuffer(marshaledBody))
	if satToken != "" {
		req.Header.Set("Authorization", satToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return emperror.WrapWith(err, "failed to make http request")
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return emperror.WrapWith(err, "failed to read body")
	}

	if resp.StatusCode != 200 {
		return emperror.WrapWith(fmt.Errorf("unable to register webhook"), "received non-200 response", "code", resp.StatusCode, "body", string(respBody[:]))
	}
	return nil
}
