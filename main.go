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
	"github.com/Comcast/webpa-common/xmetrics"
	"github.com/go-kit/kit/metrics/provider"
	olog "log"
	"net/http"
	_ "net/http/pprof"

	"github.com/Comcast/codex/blacklist"

	"github.com/Comcast/codex/cipher"

	"github.com/go-kit/kit/log"

	"github.com/Comcast/codex/db"
	"github.com/Comcast/codex/healthlogger"
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

	"github.com/Comcast/webpa-common/logging/logginghttp"
	"github.com/Comcast/webpa-common/server"
	"github.com/Comcast/webpa-common/xhttp/xcontext"
	"github.com/Comcast/wrp-go/wrp"

	"github.com/InVisionApp/go-health"
	"github.com/InVisionApp/go-health/handlers"
)

const (
	applicationName, apiBase  = "svalinn", "/api/v1"
	DEFAULT_KEY_ID            = "current"
	applicationVersion        = "0.8.0"
	defaultMinParseQueueSize  = 5
	defaultMinInsertQueueSize = 5
)

type SvalinnConfig struct {
	Endpoint          string
	ParseQueueSize    int
	InsertQueueSize   int
	MaxParseWorkers   int
	MaxInsertWorkers  int
	MaxBatchSize      int
	MaxBatchWaitTime  time.Duration
	PayloadMaxSize    int
	MetadataMaxSize   int
	InsertRetries     int
	DefaultTTL        time.Duration
	RetryInterval     time.Duration
	Db                db.Config
	Webhook           WebhookConfig
	RegexRules        []RuleConfig
	Health            HealthConfig
	BlacklistInterval time.Duration
}

type HealthConfig struct {
	Port     string
	Endpoint string
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
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize application: %s\n", err.Error())
		return 1
	}

	printVer := f.BoolP("version", "v", false, "displays the version number")
	if err := f.Parse(arguments); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse arguments: %s\n", err.Error())
		return 1
	}

	if *printVer {
		fmt.Println(applicationVersion)
		return 0
	}

	logging.Info(logger).Log(logging.MessageKey(), "Successfully loaded config file", "configurationFile", v.ConfigFileUsed())
	config := parseConfig(v, codex.Server)

	app, dbConn, serverHealth, requestParser, batchInserter, stopUpdateBlackList, err := createTasks(logger, config, metricsRegistry, v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to create task (refer to the log file): %s\n", err)
		return 1
	}

	// Create router
	customLogInfo := xcontext.Populate(
		logginghttp.SetLogger(logger,
			logginghttp.RequestInfo,
		),
		gokithttp.PopulateRequestContext,
	)

	svalinnHandler := alice.New(SetLogger(logger), customLogInfo)
	router := mux.NewRouter()

	// TODO: Fix Caduceus actual register
	router.Handle(apiBase+config.Endpoint, svalinnHandler.ThenFunc(app.handleWebhook))

	startServer(config, serverHealth, logger, codex, metricsRegistry, router, start, stopUpdateBlackList, requestParser, batchInserter, dbConn)
	return 0
}

func startServer(config *SvalinnConfig, serverHealth *health.Health, logger log.Logger, codex *server.WebPA, metricsRegistry xmetrics.Registry, router *mux.Router, start time.Time, stopUpdateBlackList chan struct{}, parser requestParser, inserter batchInserter, dbConn *db.Connection) {
	err := startHealthEndpoint(config, serverHealth, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to start health endoint: %s\n", err)
		return
	}
	// MARK: Starting the server
	_, runnable, done := codex.Prepare(logger, nil, metricsRegistry, router)
	waitGroup, shutdown, err := concurrent.Execute(runnable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to start device manager: %s\n", err)
		return
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
	err = serverHealth.Stop()
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "stopping health endpoint failed",
			logging.ErrorKey(), err.Error())
	}
	close(stopUpdateBlackList)
	close(shutdown)
	waitGroup.Wait()
	close(parser.requestQueue)
	parser.wg.Wait()
	close(inserter.insertQueue)
	inserter.wg.Wait()
	err = dbConn.Close()
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "closing database threads failed",
			logging.ErrorKey(), err.Error())
	}
}

func parseConfig(v *viper.Viper, defaultServer string) *SvalinnConfig {
	config := new(SvalinnConfig)
	v.Unmarshal(config)
	if config.ParseQueueSize < defaultMinParseQueueSize {
		config.ParseQueueSize = defaultMinParseQueueSize
	}
	if config.InsertQueueSize < defaultMinInsertQueueSize {
		config.InsertQueueSize = defaultMinInsertQueueSize
	}
	if config.Webhook.URL == "" {
		config.Webhook.URL = defaultServer + apiBase + config.Endpoint
	}
	return config
}

func createTasks(logger log.Logger, config *SvalinnConfig, metricsRegistry provider.Provider, v *viper.Viper) (app *App, dbConn *db.Connection, serverHealth *health.Health, rparser requestParser, bInserter batchInserter, stopUpdateBlackList chan struct{}, err error) {
	requestQueue := make(chan wrp.Message, config.ParseQueueSize)
	insertQueue := make(chan db.Record, config.InsertQueueSize)

	serverHealth = health.New()
	serverHealth.Logger = healthlogger.NewHealthLogger(logger)

	dbConn, err = db.CreateDbConnection(config.Db, metricsRegistry, serverHealth)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to initialize database connection",
			logging.ErrorKey(), err.Error())
		return
	}

	cipherOptions, err := cipher.FromViper(v)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to initialize cipher options",
			logging.ErrorKey(), err.Error())
		return
	}

	encrypter, err := cipherOptions.GetEncrypter(logger)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to load cipher encrypter",
			logging.ErrorKey(), err.Error())
		return
	}
	inserter := db.CreateRetryInsertService(dbConn, db.WithRetries(config.InsertRetries), db.WithInterval(config.RetryInterval), db.WithMeasures(metricsRegistry))

	measures := NewMeasures(metricsRegistry)

	secretGetter := startRegistration(config, logger)

	app = &App{
		logger:       logger,
		requestQueue: requestQueue,
		secretGetter: secretGetter,
		measures:     measures,
	}

	stopUpdateBlackList = make(chan struct{}, 1)
	blacklistConfig := blacklist.RefresherConfig{
		Logger:         logger,
		UpdateInterval: config.BlacklistInterval,
	}
	rules, err := createRules(config.RegexRules)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to load rules",
			logging.ErrorKey(), err.Error())
		return
	}

	rparser = requestParser{
		encrypter:       encrypter,
		blacklist:       blacklist.NewListRefresher(blacklistConfig, dbConn, stopUpdateBlackList),
		rules:           rules,
		payloadMaxSize:  config.PayloadMaxSize,
		metadataMaxSize: config.MetadataMaxSize,
		defaultTTL:      config.DefaultTTL,
		requestQueue:    requestQueue,
		insertQueue:     insertQueue,
		maxParseWorkers: config.MaxParseWorkers,
		measures:        measures,
		logger:          logger,
	}
	err = rparser.validateAndStartParser()
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to validate and start parser",
			logging.ErrorKey(), err.Error())
		return
	}
	bInserter = batchInserter{
		inserter:         inserter,
		logger:           logger,
		insertQueue:      insertQueue,
		maxInsertWorkers: config.MaxInsertWorkers,
		maxBatchSize:     config.MaxBatchSize,
		maxBatchWaitTime: config.MaxBatchWaitTime,
		measures:         measures,
	}
	err = bInserter.validateAndStartInserter()
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to validate and start inserter",
			logging.ErrorKey(), err.Error())
		return
	}

	return
}

func createWorkers(logger log.Logger, config *SvalinnConfig, encrypter cipher.Encrypt, dbConn *db.Connection, rules []rule, measures *Measures, inserter db.RetryInsertService) (chan struct{}, requestParser, batchInserter) {
	requestQueue := make(chan wrp.Message, config.ParseQueueSize)
	insertQueue := make(chan db.Record, config.InsertQueueSize)
	stopUpdateBlackList := make(chan struct{}, 1)
	blacklistConfig := blacklist.RefresherConfig{
		Logger:         logger,
		UpdateInterval: config.BlacklistInterval,
	}
	requestParser := requestParser{
		encrypter:       encrypter,
		blacklist:       blacklist.NewListRefresher(blacklistConfig, dbConn, stopUpdateBlackList),
		rules:           rules,
		payloadMaxSize:  config.PayloadMaxSize,
		metadataMaxSize: config.MetadataMaxSize,
		defaultTTL:      config.DefaultTTL,
		requestQueue:    requestQueue,
		insertQueue:     insertQueue,
		maxParseWorkers: config.MaxParseWorkers,
		measures:        measures,
		logger:          logger,
	}
	batchInserter := batchInserter{
		inserter:         inserter,
		logger:           logger,
		insertQueue:      insertQueue,
		maxInsertWorkers: config.MaxInsertWorkers,
		maxBatchSize:     config.MaxBatchSize,
		maxBatchWaitTime: config.MaxBatchWaitTime,
		measures:         measures,
	}
	return stopUpdateBlackList, requestParser, batchInserter
}

func startHealthEndpoint(config *SvalinnConfig, serverHealth *health.Health, logger log.Logger) (err error) {
	if config.Health.Endpoint != "" && config.Health.Port != "" {
		err = serverHealth.Start()
		if err != nil {
			logging.Error(logger).Log(logging.MessageKey(), "failed to start health", logging.ErrorKey(), err)
		}
		//router.Handler(config.Health.Address, handlers)
		http.HandleFunc(config.Health.Endpoint, handlers.NewJSONHandlerFunc(serverHealth, nil))
		go func() {
			olog.Fatal(http.ListenAndServe(config.Health.Port, nil))
		}()
	}
	return err
}

func startRegistration(config *SvalinnConfig, logger log.Logger) *constantSecret {
	secretGetter := NewConstantSecret(config.Webhook.Secret)
	// if the register interval is 0, don't register
	if config.Webhook.RegistrationInterval > 0 {
		registerer := newPeriodicRegisterer(config.Webhook, secretGetter, logger)

		// then continue to register
		go registerer.registerAtInterval()
	}
	return secretGetter
}

func main() {
	os.Exit(svalinn(os.Args))
}
