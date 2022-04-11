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

package requestParser

import (
	"regexp"

	"github.com/go-kit/kit/metrics"
	"github.com/go-kit/kit/metrics/provider"
	"github.com/xmidt-org/webpa-common/v2/xmetrics"
)

const (
	ParsingQueueDepth    = "parsing_queue_depth"
	DroppedEventsCounter = "dropped_events_count"
	EventCounter         = "event_count"
)

const (
	partnerIDLabel         = "partner_id"
	eventDestLabel         = "event_destination"
	reasonLabel            = "reason"
	blackListReason        = "blacklist"
	parseFailReason        = "parsing_failed"
	marshalFailReason      = "marshaling_failed"
	encryptFailReason      = "encryption_failed"
	invalidBirthdateReason = "birthdate_too_far_in_future"
	expiredReason          = "deathdate_has_already_passed"
	queueFullReason        = "queue_full"
	insertFailReason       = "inserting_failed"
)

const (
	eventRegexTemplate = `^(?P<event>[^\/]+)\/((?P<prefix>(?i)mac|uuid|dns|serial):(?P<id>[^\/]+))\/(?P<type>[^\/\s]+)`
	noEventDestination = "no-destination"
)

func Metrics() []xmetrics.Metric {
	return []xmetrics.Metric{
		{
			Name: ParsingQueueDepth,
			Help: "The depth of the parsing queue",
			Type: "gauge",
		},
		{
			Name:       DroppedEventsCounter,
			Help:       "The total number of events dropped",
			Type:       "counter",
			LabelNames: []string{reasonLabel},
		},
		{
			Name:       EventCounter,
			Help:       "Details of incoming events",
			Type:       "counter",
			LabelNames: []string{partnerIDLabel, eventDestLabel},
		},
	}
}

type Measures struct {
	ParsingQueue       metrics.Gauge
	DroppedEventsCount metrics.Counter
	EventsCount        metrics.Counter
}

type EventTypeMetrics struct {
	Regex          *regexp.Regexp
	EventTypeIndex int
}

// NewMeasures constructs a Measures given a go-kit metrics Provider
func NewMeasures(p provider.Provider) *Measures {
	return &Measures{
		ParsingQueue:       p.NewGauge(ParsingQueueDepth),
		DroppedEventsCount: p.NewCounter(DroppedEventsCounter),
		EventsCount:        p.NewCounter(EventCounter),
	}
}
