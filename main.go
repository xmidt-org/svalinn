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
	"fmt"

	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/concurrent"
	"github.com/Comcast/webpa-common/logging"
	"github.com/goph/emperror"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	//	"github.com/Comcast/webpa-common/secure/handler"
	"os"
	"os/signal"
	"time"

	"github.com/Comcast/webpa-common/server"
	"github.com/Comcast/webpa-common/wrp"
)

const (
	applicationName, apiBase = "svalinn", "/api/v1"
	DEFAULT_KEY_ID           = "current"
	applicationVersion       = "0.0.0"
)

type SvalinnConfig struct {
	Endpoint            string
	QueueSize           int
	StateLimitPerDevice int
	PayloadMaxSize      int
	MetadataMaxSize     int
	InsertRetries       int
	PruneRetries        int
	GetRetries          int
	DefaultTTL          time.Duration
	RetryInterval       time.Duration
	Db                  db.Config
	RegexRules          []RuleConfig
}

type RuleConfig struct {
	Regex        string
	TombstoneKey string
	StorePayload bool
	RuleTTL      time.Duration
	EventType    string
}

func svalinn(arguments []string) int {
	start := time.Now()

	var (
		f, v                                = pflag.NewFlagSet(applicationName, pflag.ContinueOnError), viper.New()
		logger, metricsRegistry, codex, err = server.Initialize(applicationName, arguments, f, v)
	)

	printVer := f.BoolP("version", "v", false, "displays the version number")
	if err := f.Parse(arguments); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse arguments: %s\n", err.Error())
		return 1
	}

	if *printVer {
		fmt.Println(applicationVersion)
		return 0
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize viper: %s\n", err.Error())
		return 1
	}
	logging.Info(logger).Log(logging.MessageKey(), "Successfully loaded config file", "configurationFile", v.ConfigFileUsed())

	/*validator, err := server.GetValidator(v, DEFAULT_KEY_ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Validator error: %v\n", err)
		return 1
	}*/

	config := new(SvalinnConfig)
	v.Unmarshal(config)

	requestQueue := make(chan wrp.Message, config.QueueSize)
	pruneQueue := make(chan string, config.QueueSize)

	/*authHandler := handler.AuthorizationHandler{
		HeaderName:          "Authorization",
		ForbiddenStatusCode: 403,
		Validator:           validator,
		Logger:              logger,
	}*/

	dbConn, err := db.CreateDbConnection(config.Db)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to initialize database connection",
			logging.ErrorKey(), err.Error())
		fmt.Fprintf(os.Stderr, "Database Initialize Failed: %#v\n", err)
		return 2
	}

	webhookConfig := &Webhook{
		Logger: logger,
		URL:    codex.Server + apiBase + config.Endpoint,
	}
	v.UnmarshalKey("webhook", webhookConfig)
	go func() {
		if webhookConfig.RegistrationInterval > 0 {
			err := webhookConfig.Register()
			if err != nil {
				logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to register webhook",
					logging.ErrorKey(), err.Error())
			} else {
				logging.Info(logger).Log(logging.MessageKey(), "Successfully registered webhook")
			}
			hookagain := time.NewTicker(webhookConfig.RegistrationInterval)
			for range hookagain.C {
				err := webhookConfig.Register()
				if err != nil {
					logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to register webhook",
						logging.ErrorKey(), err.Error())
				} else {
					logging.Info(logger).Log(logging.MessageKey(), "Successfully registered webhook")
				}
			}
		}
	}()

	svalinnHandler := alice.New()
	// TODO: add authentication back
	//svalinnHandler := alice.New(authHandler.Decorate)
	router := mux.NewRouter()
	// MARK: Actual server logic

	app := &App{
		logger:       logger,
		requestQueue: requestQueue,
	}

	tombstoneRules, err := createRules(config.RegexRules)

	// TODO: Fix Caduces acutal register
	router.Handle(apiBase+config.Endpoint, svalinnHandler.ThenFunc(app.handleWebhook))

	inserter := db.CreateRetryInsertService(dbConn, config.InsertRetries, config.RetryInterval)
	updater := db.CreateRetryUpdateService(dbConn, config.PruneRetries, config.RetryInterval)
	getter := db.CreateRetryEGService(dbConn, config.GetRetries, config.RetryInterval)

	requestHandler := RequestHandler{
		inserter:            inserter,
		updater:             updater,
		getter:              getter,
		logger:              logger,
		tombstoneRules:      tombstoneRules,
		payloadMaxSize:      config.PayloadMaxSize,
		metadataMaxSize:     config.MetadataMaxSize,
		stateLimitPerDevice: config.StateLimitPerDevice,
		defaultTTL:          config.DefaultTTL,
		pruneQueue:          pruneQueue,
	}
	go requestHandler.handleRequests(requestQueue)
	go requestHandler.handlePruning()

	// MARK: Starting the server
	_, runnable, done := codex.Prepare(logger, nil, metricsRegistry, router)

	waitGroup, shutdown, err := concurrent.Execute(runnable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to start device manager: %s\n", err)
		return 1
	}

	logging.Info(logger).Log(logging.MessageKey(), fmt.Sprintf("%s is up and running!", applicationName), "elapsedTime", time.Since(start))
	signals := make(chan os.Signal, 10)
	signal.Notify(signals)
	for exit := false; !exit; {
		select {
		case s := <-signals:
			if s != os.Kill && s != os.Interrupt {
				logging.Info(logger).Log(logging.MessageKey(), "ignoring signal", "signal", s)
			} else {
				logging.Error(logger).Log(logging.MessageKey(), "exiting due to signal", "signal", s)
				exit = true
			}
		case <-done:
			logging.Error(logger).Log(logging.MessageKey(), "one or more servers exited")
			exit = true
		}
	}

	close(shutdown)
	waitGroup.Wait()
	return 0
}

func main() {
	os.Exit(svalinn(os.Args))
}
