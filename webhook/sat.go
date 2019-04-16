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
	"time"
)

type SatAcquirer struct {
	Client  string        `json:"client"`
	Secret  string        `json:"secret"`
	SatURL  string        `json:"satURL"`
	Timeout time.Duration `json:"timeout"`
}

type SatToken struct {
	Expiration float64 `json:"expires_in"`
	Token      string  `json:"serviceAccessToken"`
}

func (s *SatAcquirer) Acquire() (string, error) {
	jsonStr := []byte(`{}`)
	httpclient := &http.Client{
		Timeout: s.Timeout,
	}
	req, _ := http.NewRequest("GET", s.SatURL, bytes.NewBuffer(jsonStr))
	req.Header.Set("X-Client-Id", s.Client)
	req.Header.Set("X-Client-Secret", s.Secret)

	resp, errHTTP := httpclient.Do(req)
	if errHTTP != nil {

		return "", fmt.Errorf("error acquiring SAT token: [%s]", errHTTP.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("received non 200 code acquiring SAT: code %v", resp.Status)
	}

	respBody, errRead := ioutil.ReadAll(resp.Body)
	if errRead != nil {

		return "", fmt.Errorf("error reading SAT token: [%s]", errRead.Error())
	}

	var sat SatToken

	if errUnmarshal := json.Unmarshal(respBody, &sat); errUnmarshal != nil {
		return "", fmt.Errorf("unable to read json in SAT response: [%s]", errUnmarshal.Error())
	}
	return fmt.Sprintf("Bearer %s", sat.Token), nil
}
