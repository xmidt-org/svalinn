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
	"net/http"
	"time"

	"github.com/Comcast/codex-svalinn/webhook"
	"github.com/Comcast/webpa-common/logging"
	"github.com/goph/emperror"

	"github.com/go-kit/kit/log"
)

type webhookRegisterer interface {
	Register(client *http.Client, secret string) error
}

// add in a secret getter now so that later we can just plug it in
type secretGetter interface {
	GetSecret() (string, error)
}

type WebhookConfig struct {
	RegistrationInterval time.Duration
	URL                  string
	Timeout              time.Duration
	RegistrationURL      string
	EventsToWatch        []string
	AcquirerConfig       map[string]interface{}
	Secret               string
}

type periodicRegisterer struct {
	registerer           webhookRegisterer
	secretGetter         secretGetter
	registrationInterval time.Duration
	timeout              time.Duration
	logger               log.Logger
}

func newPeriodicRegisterer(wc WebhookConfig, secretGetter secretGetter, logger log.Logger) periodicRegisterer {
	acquirer, err := determineTokenAcquirer(wc.AcquirerConfig)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to parse token acquirer config, using default acquirer",
			logging.ErrorKey(), err.Error(), "config", wc.AcquirerConfig)
	}
	webhook := webhook.Webhook{
		URL:             wc.URL,
		RegistrationURL: wc.RegistrationURL,
		EventsToWatch:   wc.EventsToWatch,
		Logger:          logger,
		Acquirer:        acquirer,
	}
	return periodicRegisterer{
		registerer:           &webhook,
		secretGetter:         secretGetter,
		registrationInterval: wc.RegistrationInterval,
		timeout:              wc.Timeout,
		logger:               logger,
	}
}

// determineTokenAcquirer always returns a valid TokenAcquirer, but may also return an error
func determineTokenAcquirer(config map[string]interface{}) (webhook.TokenAcquirer, error) {
	defaultAcquirer := &webhook.DefaultAcquirer{}
	if value, ok := config["sat"]; ok {
		acquirer, ok := value.(webhook.SatAcquirer)
		if !ok {
			return defaultAcquirer, errors.New("Couldn't parse SAT acquirer config")
		}
		return &acquirer, nil
	}
	if value, ok := config["basic"]; ok {
		str, ok := value.(string)
		if !ok {
			return defaultAcquirer, errors.New("Couldn't parse basic acquirer config")
		}
		if str == "" {
			return defaultAcquirer, errors.New("Empty basic credentials")
		}
		return webhook.NewBasicAcquirer(str), nil
	}
	return defaultAcquirer, nil
}

func (p *periodicRegisterer) registerAtInterval() {
	hookagain := time.NewTicker(p.registrationInterval)
	err := getSecretAndRegister(p.registerer, p.secretGetter, p.timeout)
	if err != nil {
		logging.Error(p.logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to register webhook",
			logging.ErrorKey(), err.Error())
	} else {
		logging.Info(p.logger).Log(logging.MessageKey(), "Successfully registered webhook")
	}
	for range hookagain.C {
		err := getSecretAndRegister(p.registerer, p.secretGetter, p.timeout)
		if err != nil {
			logging.Error(p.logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to register webhook",
				logging.ErrorKey(), err.Error())
		} else {
			logging.Info(p.logger).Log(logging.MessageKey(), "Successfully registered webhook")
		}
	}
}

func getSecretAndRegister(registerer webhookRegisterer, secretGetter secretGetter, timeout time.Duration) error {
	httpclient := &http.Client{
		Timeout: timeout,
	}
	secret, err := secretGetter.GetSecret()
	if err != nil {
		return emperror.Wrap(err, "Failed to get secret")
	}
	err = registerer.Register(httpclient, secret)
	if err != nil {
		return err
	}
	return nil
}
