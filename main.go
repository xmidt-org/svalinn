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
	olog "log"
	"net/http"
	_ "net/http/pprof"
	"sync"

	"github.com/Comcast/codex/blacklist"

	"github.com/Comcast/codex/cipher"

	"github.com/go-kit/kit/log"

	"github.com/Comcast/codex/db"
	"github.com/Comcast/codex/healthlogger"
	"github.com/Comcast/webpa-common/concurrent"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/secure"
	"github.com/Comcast/webpa-common/xmetrics"
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

type Svalinn struct {
	measures      *Measures
	rules         []rule
	requestQueue  chan wrp.Message
	insertQueue   chan db.Record
	secretGetter  secretGetter
	encrypter     cipher.Encrypt
	done          <-chan struct{}
	shutdown      chan struct{}
	waitGroup     *sync.WaitGroup
	requestParser requestParser
	batchInserter batchInserter
}

type database struct {
	dbConn             *db.Connection
	blacklistStop      chan struct{}
	blacklistRefresher blacklist.List
	inserter           db.Inserter
	health             *health.Health
}

type HealthConfig struct {
	Port     string
	Endpoint string
}

func svalinn(arguments []string) {
	start := time.Now()

	var (
		f, v                                = pflag.NewFlagSet(applicationName, pflag.ContinueOnError), viper.New()
		logger, metricsRegistry, codex, err = server.Initialize(applicationName, arguments, f, v, secure.Metrics, db.Metrics, Metrics)
	)

	if parseErr, done := printVersion(f, arguments); done {
		// if we're done, we're exiting no matter what
		exitIfError(logger, emperror.Wrap(parseErr, "failed to parse arguments"))
		os.Exit(0)
	}

	exitIfError(logger, emperror.Wrap(err, "unable to initialize viper"))
	logging.Info(logger).Log(logging.MessageKey(), "Successfully loaded config file", "configurationFile", v.ConfigFileUsed())

	config, s := initialize(logger, v, metricsRegistry, codex.Server)

	svalinnHandler := alice.New()
	router := mux.NewRouter()
	// MARK: Actual server logic

	app := &App{
		logger:       logger,
		requestQueue: s.requestQueue,
		secretGetter: s.secretGetter,
		measures:     s.measures,
	}

	// TODO: Fix Caduces acutal register
	router.Handle(apiBase+config.Endpoint, svalinnHandler.ThenFunc(app.handleWebhook))

	database, err := setupDb(config, logger, metricsRegistry)
	exitIfError(logger, emperror.Wrap(err, "failed to initialize database connection"))

	s.requestParser = requestParser{
		encrypter:       s.encrypter,
		blacklist:       database.blacklistRefresher,
		rules:           s.rules,
		payloadMaxSize:  config.PayloadMaxSize,
		metadataMaxSize: config.MetadataMaxSize,
		defaultTTL:      config.DefaultTTL,
		requestQueue:    s.requestQueue,
		insertQueue:     s.insertQueue,
		maxParseWorkers: config.MaxParseWorkers,
		measures:        s.measures,
		logger:          logger,
	}
	s.batchInserter = batchInserter{
		inserter:         database.inserter,
		logger:           logger,
		insertQueue:      s.insertQueue,
		maxInsertWorkers: config.MaxInsertWorkers,
		maxBatchSize:     config.MaxBatchSize,
		maxBatchWaitTime: config.MaxBatchWaitTime,
		measures:         s.measures,
	}
	err = s.requestParser.validateAndStartParser()
	exitIfError(logger, emperror.Wrap(err, "failed to validate and start parser"))
	err = s.batchInserter.validateAndStartInserter()
	exitIfError(logger, emperror.Wrap(err, "failed to validate and start inserter"))

	startHealth(logger, database.health, config)

	// MARK: Starting the server
	var runnable concurrent.Runnable
	_, runnable, s.done = codex.Prepare(logger, nil, metricsRegistry, router)
	s.waitGroup, s.shutdown, err = concurrent.Execute(runnable)
	exitIfError(logger, emperror.Wrap(err, "unable to start device manager"))

	logging.Info(logger).Log(logging.MessageKey(), fmt.Sprintf("%s is up and running!", applicationName), "elapsedTime", time.Since(start))

	waitUntilShutdown(logger, s, database)
	logging.Info(logger).Log(logging.MessageKey(), "Svalinn has shut down")
}

func printVersion(f *pflag.FlagSet, arguments []string) (error, bool) {
	printVer := f.BoolP("version", "v", false, "displays the version number")
	if err := f.Parse(arguments); err != nil {
		return err, true
	}

	if *printVer {
		fmt.Println(applicationVersion)
		return nil, true
	}
	return nil, false
}

func exitIfError(logger log.Logger, err error) {
	if err != nil {
		if logger != nil {
			logging.Error(logger, emperror.Context(err)...).Log(logging.ErrorKey(), err.Error())
		}
		fmt.Fprintf(os.Stderr, "Error: %#v\n", err.Error())
		os.Exit(1)
	}
}

func initialize(logger log.Logger, v *viper.Viper, metricsRegistry xmetrics.Registry, server string) (*SvalinnConfig, *Svalinn) {
	var s Svalinn

	config := new(SvalinnConfig)
	v.Unmarshal(config)

	if config.ParseQueueSize < defaultMinParseQueueSize {
		config.ParseQueueSize = defaultMinParseQueueSize
	}
	if config.InsertQueueSize < defaultMinInsertQueueSize {
		config.InsertQueueSize = defaultMinInsertQueueSize
	}

	if config.Webhook.URL == "" {
		config.Webhook.URL = server + apiBase + config.Endpoint
	}

	s.requestQueue = make(chan wrp.Message, config.ParseQueueSize)
	s.insertQueue = make(chan db.Record, config.InsertQueueSize)

	cipherOptions, err := cipher.FromViper(v)
	exitIfError(logger, emperror.Wrap(err, "failed to initialize cipher options"))

	s.encrypter, err = cipherOptions.GetEncrypter(logger)
	exitIfError(logger, emperror.Wrap(err, "failed to load cipher encrypter"))

	// Create Metrics
	s.measures = NewMeasures(metricsRegistry)

	s.secretGetter = NewConstantSecret(config.Webhook.Secret)
	// if the register interval is 0, don't register
	if config.Webhook.RegistrationInterval > 0 {
		registerer := newPeriodicRegisterer(config.Webhook, s.secretGetter, logger)

		// then continue to register
		go registerer.registerAtInterval()
	}

	s.rules, err = createRules(config.RegexRules)
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "failed to create rules",
			logging.ErrorKey(), err.Error())
	}

	return config, &s

}

func setupDb(config *SvalinnConfig, logger log.Logger, metricsRegistry xmetrics.Registry) (database, error) {
	var (
		d   database
		err error
	)
	d.health = health.New()
	d.health.Logger = healthlogger.NewHealthLogger(logger)

	d.dbConn, err = db.CreateDbConnection(config.Db, metricsRegistry, d.health)
	if err != nil {
		return database{}, err
	}

	d.inserter = db.CreateRetryInsertService(d.dbConn, db.WithRetries(config.InsertRetries), db.WithInterval(config.RetryInterval), db.WithMeasures(metricsRegistry))

	d.blacklistStop = make(chan struct{}, 1)
	blacklistConfig := blacklist.RefresherConfig{
		Logger:         logger,
		UpdateInterval: config.BlacklistInterval,
	}
	d.blacklistRefresher = blacklist.NewListRefresher(blacklistConfig, d.dbConn, d.blacklistStop)
	return d, nil

}

func startHealth(logger log.Logger, health *health.Health, config *SvalinnConfig) {
	if config.Health.Endpoint != "" && config.Health.Port != "" {
		err := health.Start()
		if err != nil {
			logging.Error(logger).Log(logging.MessageKey(), "failed to start health", logging.ErrorKey(), err)
		}
		//router.Handler(config.Health.Address, handlers)
		http.HandleFunc(config.Health.Endpoint, handlers.NewJSONHandlerFunc(health, nil))
		go func() {
			olog.Fatal(http.ListenAndServe(config.Health.Port, nil))
		}()
	}
}

func waitUntilShutdown(logger log.Logger, s *Svalinn, database database) {
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
		case <-s.done:
			logging.Error(logger).Log(logging.MessageKey(), "one or more servers exited")
			exit = true
		}
	}

	err := database.health.Stop()
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "stopping health endpoint failed",
			logging.ErrorKey(), err.Error())
	}
	close(database.blacklistStop)
	close(s.shutdown)
	s.waitGroup.Wait()
	close(s.requestQueue)
	s.requestParser.wg.Wait()
	close(s.insertQueue)
	s.batchInserter.wg.Wait()
	err = database.dbConn.Close()
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "closing database threads failed",
			logging.ErrorKey(), err.Error())
	}
}

func main() {
	svalinn(os.Args)
}
