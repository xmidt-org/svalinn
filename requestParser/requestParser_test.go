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
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/metrics/provider"

	"github.com/xmidt-org/codex-db/blacklist"
	"github.com/xmidt-org/wrp-go/v3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/xmidt-org/voynicrypto"
	"github.com/xmidt-org/webpa-common/v2/basculechecks"
	"github.com/xmidt-org/webpa-common/v2/logging"
	"github.com/xmidt-org/webpa-common/v2/semaphore"
	"github.com/xmidt-org/webpa-common/v2/xmetrics/xmetricstest"
)

var (
	goodEvent = wrp.Message{
		Source:          "test source",
		Destination:     "/test/",
		Type:            wrp.SimpleEventMessageType,
		PartnerIDs:      []string{"test1", "test2"},
		TransactionUUID: "transaction test uuid",
		Payload:         []byte(`{"ts":"2019-02-13T21:19:02.614191735Z"}`),
		Metadata:        map[string]string{"testkey": "testvalue"},
	}
)

func TestNewRequestParser(t *testing.T) {
	goodEncrypter := new(mockEncrypter)
	goodInserter := new(mockInserter)
	goodBlacklist := new(mockBlacklist)
	goodRegistry := xmetricstest.NewProvider(nil, Metrics)
	goodMeasures := NewMeasures(goodRegistry)
	goodConfig := Config{
		QueueSize:       1000,
		MetadataMaxSize: 100000,
		PayloadMaxSize:  1000000,
		MaxWorkers:      5000,
		DefaultTTL:      5 * time.Hour,
	}
	tests := []struct {
		description           string
		encrypter             voynicrypto.Encrypt
		blacklist             blacklist.List
		inserter              inserter
		config                Config
		logger                log.Logger
		registry              provider.Provider
		expectedRequestParser *RequestParser
		expectedErr           error
	}{
		{
			description: "Success",
			encrypter:   goodEncrypter,
			blacklist:   goodBlacklist,
			inserter:    goodInserter,
			registry:    goodRegistry,
			config:      goodConfig,
			logger:      log.NewJSONLogger(os.Stdout),
			expectedRequestParser: &RequestParser{
				rc: RecordConfig{
					encrypter: goodEncrypter,
					blacklist: goodBlacklist,
					inserter:  goodInserter,
				},
				measures: goodMeasures,
				config:   goodConfig,
				logger:   log.NewJSONLogger(os.Stdout),
			},
		},
		{
			description: "Success With Defaults",
			encrypter:   goodEncrypter,
			blacklist:   goodBlacklist,
			inserter:    goodInserter,
			registry:    goodRegistry,
			config: Config{
				MetadataMaxSize: -5,
				PayloadMaxSize:  -5,
			},
			expectedRequestParser: &RequestParser{
				rc: RecordConfig{
					encrypter: goodEncrypter,
					blacklist: goodBlacklist,
					inserter:  goodInserter,
				},

				measures: goodMeasures,
				config: Config{
					QueueSize:  defaultMinQueueSize,
					DefaultTTL: defaultTTL,
					MaxWorkers: minMaxWorkers,
				},
				logger: defaultLogger,
			},
		},
		{
			description: "No Encrypter Error",
			expectedErr: errors.New("no encrypter"),
		},
		{
			description: "No Blacklist Error",
			encrypter:   goodEncrypter,
			expectedErr: errors.New("no blacklist"),
		},
		{
			description: "No Inserter Error",
			encrypter:   goodEncrypter,
			blacklist:   goodBlacklist,
			expectedErr: errors.New("no inserter"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			rp, err := NewRequestParser(tc.config, tc.logger, tc.registry, tc.inserter, tc.blacklist, tc.encrypter, nil)
			if rp != nil {
				tc.expectedRequestParser.requestQueue = rp.requestQueue
				tc.expectedRequestParser.parseWorkers = rp.parseWorkers
				tc.expectedRequestParser.eventTypeMetrics = rp.eventTypeMetrics
				rp.rc.currTime = nil
			}
			assert.Equal(tc.expectedRequestParser, rp)
			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}
		})
	}
}

func TestParseRequest(t *testing.T) {
	testassert := assert.New(t)
	goodTime, err := time.Parse(time.RFC3339Nano, "2019-02-13T21:19:02.614191735Z")
	testassert.Nil(err)

	eventRegex, eventTypeIndex := createEventTemplateRegex(eventRegexTemplate, nil)
	beginTime := time.Now()
	tests := []struct {
		description        string
		req                wrp.Message
		encryptErr         error
		insertErr          error
		expectEncryptCount float64
		expectParseCount   float64
		expectInsertCount  float64
		encryptCalled      bool
		blacklistCalled    bool
		insertCalled       bool
		timeExpected       bool
	}{
		{
			description:     "Success",
			req:             goodEvent,
			encryptCalled:   true,
			blacklistCalled: true,
			insertCalled:    true,
			timeExpected:    true,
		},
		{
			description:      "Empty ID Error",
			expectParseCount: 1.0,
		},
		{
			description:        "Encrypt Error",
			req:                goodEvent,
			encryptErr:         errors.New("encrypt failed"),
			expectEncryptCount: 1.0,
			encryptCalled:      true,
			blacklistCalled:    true,
			timeExpected:       true,
		},
		{
			description:       "Insert Error",
			req:               goodEvent,
			insertErr:         errors.New("insert failed"),
			expectInsertCount: 1.0,
			encryptCalled:     true,
			insertCalled:      true,
			blacklistCalled:   true,
			timeExpected:      true,
		},
		{
			description: "Event Metrics – Random Event",
			req: wrp.Message{
				Source:          goodEvent.Source,
				Destination:     "device-status/mac:some_random_mac_address/an-event/some_timestamp",
				Type:            goodEvent.Type,
				PartnerIDs:      goodEvent.PartnerIDs,
				TransactionUUID: goodEvent.TransactionUUID,
				Payload:         goodEvent.Payload,
				Metadata:        goodEvent.Metadata,
			},
			encryptCalled:   true,
			blacklistCalled: true,
			insertCalled:    true,
			timeExpected:    true,
		},
		{
			description: "Event Metrics – No Partner ID",
			req: wrp.Message{
				Source:          goodEvent.Source,
				Destination:     "device-status/mac:some_random_mac_address/an-event/some_timestamp",
				Type:            goodEvent.Type,
				PartnerIDs:      []string{},
				TransactionUUID: goodEvent.TransactionUUID,
				Payload:         goodEvent.Payload,
				Metadata:        goodEvent.Metadata,
			},
			encryptCalled:   true,
			blacklistCalled: true,
			insertCalled:    true,
			timeExpected:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			encrypter := new(mockEncrypter)
			if tc.encryptCalled {
				encrypter.On("EncryptMessage", mock.Anything).Return(tc.encryptErr)
			}

			mblacklist := new(mockBlacklist)
			if tc.blacklistCalled {
				mblacklist.On("InList", mock.Anything).Return("", false).Once()
			}

			mockInserter := new(mockInserter)
			if tc.insertCalled {
				mockInserter.On("Insert", mock.Anything).Return(tc.insertErr).Once()
			}

			mockTimeTracker := new(mockTimeTracker)
			if !tc.insertCalled {
				mockTimeTracker.On("TrackTime", mock.Anything).Once()
			}

			p := xmetricstest.NewProvider(nil, Metrics)
			m := NewMeasures(p)

			timeCalled := false
			timeFunc := func() time.Time {
				timeCalled = true
				return goodTime
			}

			handler := RequestParser{
				rc: RecordConfig{
					encrypter:   encrypter,
					inserter:    mockInserter,
					timeTracker: mockTimeTracker,
					blacklist:   mblacklist,
					currTime:    timeFunc,
				},
				config: Config{
					PayloadMaxSize:  9999,
					MetadataMaxSize: 9999,
					DefaultTTL:      time.Second,
					MaxWorkers:      5,
				},
				parseWorkers:     semaphore.New(2),
				measures:         m,
				logger:           logging.NewTestLogger(nil, t),
				eventTypeMetrics: EventTypeMetrics{Regex: eventRegex, EventTypeIndex: eventTypeIndex},
			}

			handler.parseWorkers.Acquire()
			handler.parseRequest(WrpWithTime{Message: tc.req, Beginning: beginTime})
			mockInserter.AssertExpectations(t)
			mblacklist.AssertExpectations(t)
			encrypter.AssertExpectations(t)
			mockTimeTracker.AssertExpectations(t)
			p.Assert(t, DroppedEventsCounter, reasonLabel, encryptFailReason)(xmetricstest.Value(tc.expectEncryptCount))
			p.Assert(t, DroppedEventsCounter, reasonLabel, parseFailReason)(xmetricstest.Value(tc.expectParseCount))
			p.Assert(t, DroppedEventsCounter, reasonLabel, insertFailReason)(xmetricstest.Value(tc.expectInsertCount))
			p.Assert(t, EventCounter, partnerIDLabel, basculechecks.DeterminePartnerMetric(tc.req.PartnerIDs), eventDestLabel, getEventDestinationType(handler.eventTypeMetrics.Regex, handler.eventTypeMetrics.EventTypeIndex, tc.req.Destination))(xmetricstest.Value(1.0))
			testassert.Equal(tc.timeExpected, timeCalled)

		})
	}
}

func TestCreateEventTemplateRegex(t *testing.T) {
	tests := []struct {
		description   string
		regex         string
		validRegex    bool
		expectedIndex int
	}{
		{
			description:   "Valid Regex",
			regex:         `^(?P<event>[^\/]+)\/((?P<prefix>(?i)mac|uuid|dns|serial):(?P<id>[^\/]+))\/(?P<type>[^\/\s]+)`,
			validRegex:    true,
			expectedIndex: 5,
		},
		{
			description:   "Invalid Regex",
			regex:         `^(?<event>[^\/]+\/`,
			validRegex:    false,
			expectedIndex: -1,
		},
		{
			description:   "No Type Regex",
			regex:         `^(?P<event>[^\/]+)\/((?P<prefix>(?i)mac|uuid|dns|serial):(?P<id>[^\/]+))`,
			validRegex:    true,
			expectedIndex: -1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			compiledRegex, index := createEventTemplateRegex(tc.regex, nil)
			if tc.validRegex {
				assert.NotNil(compiledRegex)
			} else {
				assert.Nil(compiledRegex)
			}
			assert.Equal(tc.expectedIndex, index)
		})
	}
}

func TestGetEventDestinationType(t *testing.T) {
	eventRegex := regexp.MustCompile(eventRegexTemplate)
	typeIndex := -1

	for i, name := range eventRegex.SubexpNames() {
		if name == "type" {
			typeIndex = i
		}
	}

	tests := []struct {
		description           string
		destination           string
		expectDestinationType string
		noCompiledRegex       bool
	}{
		{
			description:           "Online Event",
			destination:           "device-status/mac:some_random_mac_address/online",
			expectDestinationType: "online",
		},
		{
			description:           "Offline Event",
			destination:           "device-status/mac:some_random_mac_address/offline",
			expectDestinationType: "offline",
		},
		{
			description:           "Fully Manageable Event",
			destination:           "device-status/mac:some_random_mac_address/fully-manageable/some_timestamp",
			expectDestinationType: "fully-manageable",
		},
		{
			description:           "Operational Event",
			destination:           "device-status/mac:some_random_mac_address/operational/some_timestamp",
			expectDestinationType: "operational",
		},
		{
			description:           "Reboot Pending Event",
			destination:           "device-status/mac:some_random_mac_address/reboot-pending/some_timestamp",
			expectDestinationType: "reboot-pending",
		},
		{
			description:           "Random Event",
			destination:           "device-status/mac:some_random_mac_address/some-random-event/some_timestamp",
			expectDestinationType: "some-random-event",
		},
		{
			description:           "No Event Destination",
			destination:           "",
			expectDestinationType: noEventDestination,
		},
		{
			description:           "Unexpected Event Structure",
			destination:           "some-event/random-event-that-doesn't-match-expected-structure",
			expectDestinationType: noEventDestination,
		},
		{
			description:           "No Regex",
			destination:           "device-status/mac:some_random_mac_address/some-random-event/some_timestamp",
			expectDestinationType: noEventDestination,
			noCompiledRegex:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			if tc.noCompiledRegex {
				assert.Equal(t, tc.expectDestinationType, getEventDestinationType(nil, typeIndex, tc.destination))
			} else {
				assert.Equal(t, tc.expectDestinationType, getEventDestinationType(eventRegex, typeIndex, tc.destination))
			}

		})
	}
}
