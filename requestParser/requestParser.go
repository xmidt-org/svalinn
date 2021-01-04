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
	errQueueFull         = errors.New("Queue Full")

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

type RequestParser struct {
	encrypter        voynicrypto.Encrypt
	blacklist        blacklist.List
	inserter         inserter
	timeTracker      TimeTracker
	rules            rules.Rules
	requestQueue     chan WrpWithTime
	parseWorkers     semaphore.Interface
	wg               sync.WaitGroup
	measures         *Measures
	logger           log.Logger
	config           Config
	currTime         func() time.Time
	eventTypeMetrics EventTypeMetrics
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

	r := RequestParser{
		config:           config,
		logger:           logger,
		measures:         measures,
		parseWorkers:     workers,
		requestQueue:     queue,
		inserter:         inserter,
		rules:            rules,
		blacklist:        blacklist,
		encrypter:        encrypter,
		currTime:         time.Now,
		timeTracker:      timeTracker,
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

	rule, err := r.rules.FindRule(request.Message.Destination)
	if err != nil {
		logging.Info(r.logger).Log(logging.MessageKey(), "Could not get rule", logging.ErrorKey(), err, "destination", request.Message.Destination)
	}

	eventType := db.Default
	if rule != nil {
		eventType = db.ParseEventType(rule.EventType())
	}

	record, reason, err := r.createRecord(request.Message, rule, eventType)
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, reason).Add(1.0)
		if reason == blackListReason {
			logging.Info(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
				"Failed to create record", logging.ErrorKey(), err.Error())
			r.timeTracker.TrackTime(time.Since(request.Beginning))
			return
		}
		logging.Warn(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to create record", logging.ErrorKey(), err.Error())
		r.timeTracker.TrackTime(time.Since(request.Beginning))
		return
	}

	err = r.inserter.Insert(batchInserter.RecordWithTime{Record: record, Beginning: request.Beginning})
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, insertFailReason).Add(1.0)
		logging.Warn(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to insert record", logging.ErrorKey(), err.Error())
	}
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
