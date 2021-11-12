/**
 * Copyright 2021 Comcast Cable Communications Management, LLC
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

package requestParser

import (
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/go-kit/kit/metrics/provider"

	"github.com/xmidt-org/svalinn/rules"

	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
	db "github.com/xmidt-org/codex-db"
	"github.com/xmidt-org/codex-db/batchInserter"
	"github.com/xmidt-org/codex-db/blacklist"
	"github.com/xmidt-org/voynicrypto"
	"github.com/xmidt-org/webpa-common/basculechecks"
	"github.com/xmidt-org/webpa-common/logging"
	"github.com/xmidt-org/webpa-common/semaphore"
	"github.com/xmidt-org/wrp-go/v2"
)

var (
	errEmptyID           = errors.New("empty id is invalid")
	errUnexpectedWRPType = errors.New("unexpected wrp message type")
	errFutureBirthdate   = errors.New("birthdate is too far in the future")
	errExpired           = errors.New("deathdate has passed")
	errBlacklist         = errors.New("device is in blacklist")
	errQueueFull         = errors.New("queue full")

	defaultLogger = log.NewNopLogger()
)

const (
	defaultTTL          = time.Duration(5) * time.Minute
	minMaxWorkers       = 5
	defaultMinQueueSize = 5
)

type TimeTracker interface {
	TrackTime(time.Duration)
}

type inserter interface {
	Insert(record batchInserter.RecordWithTime) error
}

type Config struct {
	MetadataMaxSize int
	PayloadMaxSize  int
	QueueSize       int
	MaxWorkers      int
	DefaultTTL      time.Duration
	RegexRules      []rules.RuleConfig
}

type RecordConfig struct {
	inserter    inserter
	blacklist   blacklist.List
	currTime    func() time.Time
	rules       rules.Rules
	timeTracker TimeTracker
	encrypter   voynicrypto.Encrypt
}

type RequestParser struct {
	rc               RecordConfig
	config           Config
	logger           log.Logger
	parseWorkers     semaphore.Interface
	wg               sync.WaitGroup
	measures         *Measures
	eventTypeMetrics EventTypeMetrics
	requestQueue     chan WrpWithTime
}

type WrpWithTime struct {
	Message   wrp.Message
	Beginning time.Time
}

func NewRequestParser(config Config, logger log.Logger, metricsRegistry provider.Provider, inserter inserter, blacklist blacklist.List, encrypter voynicrypto.Encrypt, timeTracker TimeTracker) (*RequestParser, error) {
	if encrypter == nil {
		return nil, errors.New("no encrypter")
	}
	if blacklist == nil {
		return nil, errors.New("no blacklist")
	}
	if inserter == nil {
		return nil, errors.New("no inserter")
	}
	rules, err := rules.NewRules(config.RegexRules)
	if err != nil {
		return nil, emperror.Wrap(err, "failed to create rules from config")
	}

	if config.DefaultTTL == 0 {
		config.DefaultTTL = defaultTTL
	}
	if config.MetadataMaxSize < 0 {
		config.MetadataMaxSize = 0
	}
	if config.PayloadMaxSize < 0 {
		config.PayloadMaxSize = 0
	}
	if config.MaxWorkers < minMaxWorkers {
		config.MaxWorkers = minMaxWorkers
	}

	if config.QueueSize < defaultMinQueueSize {
		config.QueueSize = defaultMinQueueSize
	}
	if logger == nil {
		logger = defaultLogger
	}

	var measures *Measures
	if metricsRegistry != nil {
		measures = NewMeasures(metricsRegistry)
	}
	queue := make(chan WrpWithTime, config.QueueSize)
	workers := semaphore.New(config.MaxWorkers)
	template, typeIndex := createEventTemplateRegex(eventRegexTemplate, logger)
	recordConfig := RecordConfig{
		inserter:    inserter,
		rules:       rules,
		blacklist:   blacklist,
		encrypter:   encrypter,
		currTime:    time.Now,
		timeTracker: timeTracker,
	}

	r := RequestParser{
		rc:               recordConfig,
		config:           config,
		logger:           logger,
		measures:         measures,
		parseWorkers:     workers,
		requestQueue:     queue,
		eventTypeMetrics: EventTypeMetrics{Regex: template, EventTypeIndex: typeIndex},
	}

	return &r, nil
}

func (r *RequestParser) Start() {
	r.wg.Add(1)
	go r.parseRequests()
}

func (r *RequestParser) Parse(wrpWithTime WrpWithTime) (err error) {
	select {
	case r.requestQueue <- wrpWithTime:
		if r.measures != nil {
			r.measures.ParsingQueue.Add(1.0)
		}
	default:
		if r.measures != nil {
			r.measures.DroppedEventsCount.With(reasonLabel, queueFullReason).Add(1.0)
		}
		err = errQueueFull
	}
	return
}

func (r *RequestParser) Stop() {
	close(r.requestQueue)
	r.wg.Wait()
}

func (r *RequestParser) parseRequests() {
	defer r.wg.Done()
	for request := range r.requestQueue {
		if r.measures != nil {
			r.measures.ParsingQueue.Add(-1.0)
		}
		r.parseWorkers.Acquire()
		go r.parseRequest(request)
	}

	// Grab all the workers to make sure they are done.
	for i := 0; i < r.config.MaxWorkers; i++ {
		r.parseWorkers.Acquire()
	}
}

func (r *RequestParser) parseRequest(request WrpWithTime) {
	defer r.parseWorkers.Release()

	//use regex matching to see what event type event is, for events metrics
	eventDestination := getEventDestinationType(r.eventTypeMetrics.Regex, r.eventTypeMetrics.EventTypeIndex, request.Message.Destination)

	partnerID := basculechecks.DeterminePartnerMetric(request.Message.PartnerIDs)

	r.measures.EventsCount.With(partnerIDLabel, partnerID, eventDestLabel, eventDestination).Add(1.0)

	rule, err := r.rc.rules.FindRule(request.Message.Destination)
	if err != nil {
		logging.Info(r.logger).Log(logging.MessageKey(), "Could not get rule", logging.ErrorKey(), err, "destination", request.Message.Destination)
	}

	eventType := db.Default
	if rule != nil {
		eventType = db.ParseEventType(rule.EventType())
	}

	r.recordHandler(request, eventType, rule)
}

func (r *RequestParser) recordHandler(request WrpWithTime, eventType db.EventType, rule *rules.Rule) {
	record, reason, err := r.createRecord(request.Message, rule, eventType)
	if err != nil {
		r.handleCreateRecordErr(request, record, reason, err)
		return
	}

	err = r.rc.inserter.Insert(batchInserter.RecordWithTime{Record: record, Beginning: request.Beginning})
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, insertFailReason).Add(1.0)
		logging.Warn(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to insert record", logging.ErrorKey(), err.Error())
	}
}

func (r *RequestParser) handleCreateRecordErr(request WrpWithTime, record db.Record, reason string, err error) {
	r.measures.DroppedEventsCount.With(reasonLabel, reason).Add(1.0)
	if reason == blackListReason {
		logging.Info(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to create record", logging.ErrorKey(), err.Error())
		r.rc.timeTracker.TrackTime(time.Since(request.Beginning))
		return
	}
	logging.Warn(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
		"Failed to create record", logging.ErrorKey(), err.Error())
	r.rc.timeTracker.TrackTime(time.Since(request.Beginning))
}

// create compiled regex for events regex template
func createEventTemplateRegex(regexTemplate string, logger log.Logger) (*regexp.Regexp, int) {
	template, err := regexp.Compile(regexTemplate)

	if err != nil {
		if logger != nil {
			logging.Info(logger).Log(logging.MessageKey(), "Could not compile template regex for events", logging.ErrorKey(), err, "regex: ", regexTemplate)
		}
		return nil, -1
	}

	for i, name := range template.SubexpNames() {
		if name == "type" {
			return template, i
		}
	}

	return template, -1
}

// get specific event type
func getEventDestinationType(regexTemplate *regexp.Regexp, index int, destinationToCheck string) string {
	if regexTemplate == nil || len(destinationToCheck) == 0 {
		return noEventDestination
	}

	match := regexTemplate.FindStringSubmatch(destinationToCheck)

	if index >= 0 && index < len(match) {
		return match[index]
	}

	return noEventDestination
}
