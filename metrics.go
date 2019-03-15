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
	"github.com/Comcast/webpa-common/xmetrics"
	"github.com/go-kit/kit/metrics"
	"github.com/go-kit/kit/metrics/provider"
)

const (
	IncomingQueueDepth   = "incoming_queue_depth"
	DroppedEventsCounter = "dropped_events_count"
)

const (
	reasonLabel            = "reason"
	parseFailReason        = "parsing failed"
	marshalFailReason      = "marshaling failed"
	invalidBirthdateReason = "invalid birthdate"
	dbFailReason           = "database request failed"
)

func Metrics() []xmetrics.Metric {
	return []xmetrics.Metric{
		{
			Name: IncomingQueueDepth,
			Help: "The depth of the queue",
			Type: "gauge",
		},
		{
			Name: DroppedEventsCounter,
			Help: "The total number of events dropped",
			Type: "counter",
		},
	}
}

type Measures struct {
	DepthQueue         metrics.Gauge
	DroppedEventsCount metrics.Counter
}

// NewMeasures constructs a Measures given a go-kit metrics Provider
func NewMeasures(p provider.Provider) *Measures {
	return &Measures{
		DepthQueue:         p.NewGauge(IncomingQueueDepth),
		DroppedEventsCount: p.NewCounter(DroppedEventsCounter),
	}
}
