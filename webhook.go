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
	Sat                  webhook.SatAcquirer
	Basic                string
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
	acquirer := determineTokenAcquirer(wc)
	logging.Debug(logger).Log(logging.MessageKey(), "determined token acquirer", "acquirer", acquirer,
		"config", wc)
	webhook := webhook.Webhook{
		URL:             wc.URL,
		RegistrationURL: wc.RegistrationURL,
		EventsToWatch:   wc.EventsToWatch,
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
func determineTokenAcquirer(config WebhookConfig) webhook.TokenAcquirer {
	defaultAcquirer := &webhook.DefaultAcquirer{}
	if config.Sat.Client != "" && config.Sat.SatURL != "" && config.Sat.Secret != "" && config.Sat.Timeout != time.Duration(0)*time.Second {
		return &config.Sat
	}

	if config.Basic != "" {
		return webhook.NewBasicAcquirer(config.Basic)
	}

	return defaultAcquirer
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
