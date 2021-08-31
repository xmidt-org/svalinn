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
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	olog "log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"time"

	"github.com/InVisionApp/go-health/v2"
	"github.com/InVisionApp/go-health/v2/handlers"
	"github.com/cenkalti/backoff/v3"
	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/xmidt-org/bascule/acquire"
	"github.com/xmidt-org/bascule/basculehttp"
	db "github.com/xmidt-org/codex-db"
	"github.com/xmidt-org/codex-db/batchInserter"
	"github.com/xmidt-org/codex-db/blacklist"
	"github.com/xmidt-org/codex-db/cassandra"
	"github.com/xmidt-org/codex-db/healthlogger"
	dbretry "github.com/xmidt-org/codex-db/retry"
	"github.com/xmidt-org/svalinn/requestParser"
	"github.com/xmidt-org/voynicrypto"
	"github.com/xmidt-org/webpa-common/basculechecks"
	"github.com/xmidt-org/webpa-common/basculemetrics"
	"github.com/xmidt-org/webpa-common/concurrent"
	"github.com/xmidt-org/webpa-common/logging"
	"github.com/xmidt-org/webpa-common/server"
	"github.com/xmidt-org/webpa-common/xmetrics"
	webhook "github.com/xmidt-org/wrp-listener"
	"github.com/xmidt-org/wrp-listener/hashTokenFactory"
	secretGetter "github.com/xmidt-org/wrp-listener/secret"
	"github.com/xmidt-org/wrp-listener/webhookClient"
)

const (
	applicationName, apiBase = "svalinn", "/api/v1"
)

var (
	GitCommit = "undefined"
	Version   = "undefined"
	BuildTime = "undefined"
)

type SvalinnConfig struct {
	Endpoint          string
	Health            HealthConfig
	Webhook           WebhookConfig
	Secret            SecretConfig
	RequestParser     requestParser.Config
	BatchInserter     batchInserter.Config
	Db                cassandra.Config
	InsertRetries     backoff.ExponentialBackOff
	BlacklistInterval time.Duration
}

type WebhookConfig struct {
	RegistrationInterval time.Duration
	Timeout              time.Duration
	RegistrationURL      string
	HostToRegister       string
	Request              webhook.W
	JWT                  acquire.RemoteBearerTokenAcquirerOptions
	Basic                string
}

type SecretConfig struct {
	Header    string
	Delimiter string
}

type Svalinn struct {
	done          <-chan struct{}
	shutdown      chan struct{}
	waitGroup     *sync.WaitGroup
	requestParser *requestParser.RequestParser
	batchInserter *batchInserter.BatchInserter
	registerer    *webhookClient.PeriodicRegisterer
}

type database struct {
	dbClose            func() error
	blacklistStop      chan struct{}
	blacklistRefresher blacklist.List
	inserter           db.Inserter
	health             *health.Health
}

type HealthConfig struct {
	Port     string
	Endpoint string
}

func SetLogger(logger log.Logger) func(delegate http.Handler) http.Handler {
	return func(delegate http.Handler) http.Handler {
		return http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				ctx := r.WithContext(logging.WithLogger(r.Context(),
					log.With(logger, "requestHeaders", r.Header, "requestURL", r.URL.EscapedPath(), "method", r.Method)))
				delegate.ServeHTTP(w, ctx)
			})
	}
}

func GetLogger(ctx context.Context) log.Logger {
	return log.With(logging.GetLogger(ctx), "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
}

//nolint:funlen // this will be fixed with uber fx
func svalinn(arguments []string) {
	start := time.Now()

	var (
		f, v                                = pflag.NewFlagSet(applicationName, pflag.ContinueOnError), viper.New()
		logger, metricsRegistry, codex, err = server.Initialize(applicationName, arguments, f, v, cassandra.Metrics, dbretry.Metrics, requestParser.Metrics, batchInserter.Metrics, basculechecks.Metrics, webhookClient.Metrics, basculemetrics.Metrics, Metrics)
	)

	if parseErr, done := printVersion(f, arguments); done {
		// if we're done, we're exiting no matter what
		exitIfError(logger, emperror.Wrap(parseErr, "failed to parse arguments"))
		os.Exit(0)
	}

	exitIfError(logger, emperror.Wrap(err, "unable to initialize viper"))
	logging.Info(logger).Log(logging.MessageKey(), "Successfully loaded config file", "configurationFile", v.ConfigFileUsed())

	// set everything up
	config := new(SvalinnConfig)
	v.Unmarshal(config)

	if config.Webhook.Request.Config.URL == "" {
		config.Webhook.Request.Config.URL = codex.Server
	}
	config.Webhook.Request.Config.URL = config.Webhook.Request.Config.URL + apiBase + config.Endpoint

	cipherOptions, err := voynicrypto.FromViper(v)
	exitIfError(logger, emperror.Wrap(err, "failed to initialize cipher options"))
	encrypter, err := cipherOptions.GetEncrypter(logger)
	exitIfError(logger, emperror.Wrap(err, "failed to load cipher encrypter"))

	secretGetter := secretGetter.NewConstantSecret(config.Webhook.Request.Config.Secret)

	var m *basculemetrics.AuthValidationMeasures

	if metricsRegistry != nil {
		m = basculemetrics.NewAuthValidationMeasures(metricsRegistry)
	}
	listener := basculemetrics.NewMetricListener(m)

	svalinnHandler := alice.New()

	if config.Secret.Header != "" && config.Webhook.Request.Config.Secret != "" {
		htf, err := hashTokenFactory.New("Sha1", sha1.New, secretGetter)
		exitIfError(logger, emperror.Wrap(err, "failed to create hashTokenFactory"))

		authConstructor := basculehttp.NewConstructor(
			basculehttp.WithCLogger(GetLogger),
			basculehttp.WithTokenFactory("Sha1", htf),
			basculehttp.WithHeaderName(config.Secret.Header),
			basculehttp.WithHeaderDelimiter(config.Secret.Delimiter),
			basculehttp.WithCErrorResponseFunc(listener.OnErrorResponse),
		)

		svalinnHandler = alice.New(SetLogger(logger), authConstructor, basculehttp.NewListenerDecorator(listener))
	}
	router := mux.NewRouter()

	database, err := setupDb(config, logger, metricsRegistry)
	exitIfError(logger, emperror.Wrap(err, "failed to initialize database connection"))

	s := &Svalinn{}
	svalinnMeasures := NewMeasures(metricsRegistry)
	s.batchInserter, err = batchInserter.NewBatchInserter(config.BatchInserter, logger, metricsRegistry, database.inserter, svalinnMeasures)
	exitIfError(logger, emperror.Wrap(err, "failed to create batch inserter"))

	s.requestParser, err = requestParser.NewRequestParser(config.RequestParser, logger, metricsRegistry, s.batchInserter, database.blacklistRefresher, encrypter, svalinnMeasures)
	exitIfError(logger, emperror.Wrap(err, "failed to create request parser"))

	app := &App{
		logger: logger,
		parser: s.requestParser,
	}

	// MARK: Actual server logic
	router.Handle(apiBase+config.Endpoint, svalinnHandler.ThenFunc(app.handleWebhook))
	s.requestParser.Start()
	s.batchInserter.Start()
	startHealth(logger, database.health, config)
	// if the register interval is 0 and these values aren't set, don't register
	if config.Webhook.RegistrationInterval > 0 && config.Webhook.RegistrationURL != "" && config.Webhook.Request.Config.URL != "" && len(config.Webhook.Request.Events) > 0 {
		acquirer, err := determineTokenAcquirer(config.Webhook)
		if err != nil {
			logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to determine token acquirer", logging.ErrorKey(), err.Error())
			//TODO: we shouldn't continue trying to set the webhook registerer up if we fail
		}
		basicConfig := webhookClient.BasicConfig{
			Timeout:         config.Webhook.Timeout,
			RegistrationURL: config.Webhook.RegistrationURL,
			Request:         config.Webhook.Request,
		}
		registerer, err := webhookClient.NewBasicRegisterer(acquirer, secretGetter, basicConfig)
		if err != nil {
			logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to create basic registerer", logging.ErrorKey(), err.Error())
			//TODO: we shouldn't continue trying to set the webhook registerer up if we fail
		}
		periodicRegisterer, err := webhookClient.NewPeriodicRegisterer(registerer, config.Webhook.RegistrationInterval, logger, webhookClient.NewMeasures(metricsRegistry))
		if err != nil {
			logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to create periodic registerer", logging.ErrorKey(), err.Error())
		}

		s.registerer = periodicRegisterer
		periodicRegisterer.Start()
	}

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
		printVersionInfo(os.Stdout)
		return nil, true
	}
	return nil, false
}

func printVersionInfo(writer io.Writer) {
	fmt.Fprintf(writer, "%s:\n", applicationName)
	fmt.Fprintf(writer, "  version: \t%s\n", Version)
	fmt.Fprintf(writer, "  go version: \t%s\n", runtime.Version())
	fmt.Fprintf(writer, "  built time: \t%s\n", BuildTime)
	fmt.Fprintf(writer, "  git commit: \t%s\n", GitCommit)
	fmt.Fprintf(writer, "  os/arch: \t%s/%s\n", runtime.GOOS, runtime.GOARCH)
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

func setupDb(config *SvalinnConfig, logger log.Logger, metricsRegistry xmetrics.Registry) (database, error) {
	var (
		d database
	)
	d.health = health.New()
	d.health.Logger = healthlogger.NewHealthLogger(logger)

	dbConn, err := cassandra.CreateDbConnection(config.Db, metricsRegistry, d.health)
	if err != nil {
		return database{}, err
	}

	d.dbClose = dbConn.Close

	if config.InsertRetries.MaxElapsedTime >= 0 {
		d.inserter = dbretry.CreateRetryInsertService(
			dbConn,
			dbretry.WithBackoff(config.InsertRetries),
			dbretry.WithMeasures(metricsRegistry),
		)
	} else {
		d.inserter = dbConn
	}

	d.blacklistStop = make(chan struct{}, 1)
	blacklistConfig := blacklist.RefresherConfig{
		Logger:         logger,
		UpdateInterval: config.BlacklistInterval,
	}
	d.blacklistRefresher = blacklist.NewListRefresher(blacklistConfig, dbConn, d.blacklistStop)
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
	signal.Notify(signals, os.Kill, os.Interrupt) //nolint:staticcheck // this will be fixed with uber fx
	for exit := false; !exit; {
		select {
		case s := <-signals:
			logging.Error(logger).Log(logging.MessageKey(), "exiting due to signal", "signal", s)
			exit = true
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
	s.registerer.Stop()
	close(database.blacklistStop)
	close(s.shutdown)
	s.waitGroup.Wait()
	s.requestParser.Stop()
	s.batchInserter.Stop()
	err = database.dbClose()
	if err != nil {
		logging.Error(logger, emperror.Context(err)...).Log(logging.MessageKey(), "closing database threads failed",
			logging.ErrorKey(), err.Error())
	}
}

func main() {
	svalinn(os.Args)
}
