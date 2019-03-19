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
	"net/http"
	_ "net/http/pprof"

	"github.com/Comcast/webpa-common/semaphore"

	"github.com/go-kit/kit/log"

	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/concurrent"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/secure"
	"github.com/goph/emperror"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	//	"github.com/Comcast/webpa-common/secure/handler"
	"os"
	"os/signal"
	"time"

	gokithttp "github.com/go-kit/kit/transport/http"

	"github.com/Comcast/webpa-common/bookkeeping"
	"github.com/Comcast/webpa-common/logging/logginghttp"
	"github.com/Comcast/webpa-common/server"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/Comcast/webpa-common/xhttp/xcontext"
)

const (
	applicationName, apiBase = "svalinn", "/api/v1"
	DEFAULT_KEY_ID           = "current"
	applicationVersion       = "0.2.3"
	defaultMaxBatchSize      = 10
)

type SvalinnConfig struct {
	Endpoint         string
	ParseQueueSize   int
	InsertQueueSize  int
	MaxParseWorkers  int
	MaxInsertWorkers int
	MaxBatchSize     int
	MaxBatchWaitTime time.Duration
	PayloadMaxSize   int
	MetadataMaxSize  int
	InsertRetries    int
	PruneInterval    time.Duration
	PruneRetries     int
	DefaultTTL       time.Duration
	RetryInterval    time.Duration
	Db               db.Config
	Webhook          WebhookConfig
	RegexRules       []RuleConfig
}

func SetLogger(logger log.Logger) func(delegate http.Handler) http.Handler {
	return func(delegate http.Handler) http.Handler {
		return http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				r.WithContext(logging.WithLogger(r.Context(), logger))
				delegate.ServeHTTP(w, r.WithContext(logging.WithLogger(r.Context(), logger)))
			})
	}
}

func svalinn(arguments []string) int {
	start := time.Now()

	var (
		f, v                                = pflag.NewFlagSet(applicationName, pflag.ContinueOnError), viper.New()
		logger, metricsRegistry, codex, err = server.Initialize(applicationName, arguments, f, v, secure.Metrics, db.Metrics, Metrics)
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

	requestQueue := make(chan wrp.Message, config.ParseQueueSize)
	insertQueue := make(chan db.Record, config.InsertQueueSize)

	/*authHandler := handler.AuthorizationHandler{
		HeaderName:          "Authorization",
		ForbiddenStatusCode: 403,
		Validator:           validator,
		Logger:              logger,
	}*/

	dbConn, err := db.CreateDbConnection(config.Db, metricsRegistry)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to initialize database connection",
			logging.ErrorKey(), err.Error())
		fmt.Fprintf(os.Stderr, "Database Initialize Failed: %#v\n", err)
		return 2
	}

	// Create Metrics
	measures := NewMeasures(metricsRegistry)

	if config.Webhook.URL == "" {
		config.Webhook.URL = codex.Server + apiBase + config.Endpoint
	}
	secretGetter := NewConstantSecret(config.Webhook.Secret)
	// if the register interval is 0, don't register
	if config.Webhook.RegistrationInterval > 0 {
		registerer := newPeriodicRegisterer(config.Webhook, secretGetter, logger)

		// then continue to register
		go registerer.registerAtInterval()
	}

	customLogInfo := xcontext.Populate(0,
		logginghttp.SetLogger(logger,
			logginghttp.RequestInfo,
		),
		gokithttp.PopulateRequestContext,
	)
	// TODO: fix bookkeeping, add a decorator to add the bookkeeping requests and logger
	bookkeeper := bookkeeping.New(bookkeeping.WithResponses(bookkeeping.Code))

	svalinnHandler := alice.New(SetLogger(logger), bookkeeper, customLogInfo)
	// TODO: add authentication back
	//svalinnHandler := alice.New(authHandler.Decorate)
	router := mux.NewRouter()
	// MARK: Actual server logic

	app := &App{
		logger:       logger,
		requestQueue: requestQueue,
		secretGetter: secretGetter,
		measures:     measures,
	}

	rules, err := createRules(config.RegexRules)

	// TODO: Fix Caduces acutal register
	router.Handle(apiBase+config.Endpoint, svalinnHandler.ThenFunc(app.handleWebhook))

	inserter := db.CreateRetryInsertService(dbConn, config.InsertRetries, config.RetryInterval, metricsRegistry)
	updater := db.CreateRetryUpdateService(dbConn, config.PruneRetries, config.RetryInterval, metricsRegistry)

	if config.MaxBatchSize < 1 {
		config.MaxBatchSize = defaultMaxBatchSize
	}

	requestHandler := RequestHandler{
		inserter:         inserter,
		updater:          updater,
		logger:           logger,
		rules:            rules,
		payloadMaxSize:   config.PayloadMaxSize,
		metadataMaxSize:  config.MetadataMaxSize,
		defaultTTL:       config.DefaultTTL,
		insertQueue:      insertQueue,
		maxParseWorkers:  config.MaxParseWorkers,
		parseWorkers:     semaphore.New(config.MaxParseWorkers),
		maxInsertWorkers: config.MaxInsertWorkers,
		insertWorkers:    semaphore.New(config.MaxInsertWorkers),
		maxBatchSize:     config.MaxBatchSize,
		maxBatchWaitTime: config.MaxBatchWaitTime,
		measures:         measures,
	}
	requestHandler.wg.Add(2)
	go requestHandler.handleRequests(requestQueue)
	go requestHandler.handleRecords()
	stopPruning := make(chan struct{}, 1)
	if config.PruneInterval > 0 {
		requestHandler.wg.Add(1)
		go requestHandler.handlePruning(stopPruning, config.PruneInterval)
	}

	serverHealth := codex.Health.NewHealth(logger)

	// MARK: Starting the server
	_, runnable, done := codex.Prepare(logger, serverHealth, metricsRegistry, router)

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
	close(requestQueue)
	stopPruning <- struct{}{}
	close(requestHandler.insertQueue)
	waitGroup.Wait()
	requestHandler.wg.Wait()
	err = dbConn.Close()
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "closing database threads failed",
			logging.ErrorKey(), err.Error())
	}
	return 0
}

func main() {
	os.Exit(svalinn(os.Args))
}
