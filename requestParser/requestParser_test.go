package requestParser

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/metrics/provider"

	"github.com/Comcast/codex/blacklist"

	"github.com/Comcast/codex/cipher"
	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/semaphore"
	"github.com/Comcast/webpa-common/xmetrics/xmetricstest"
	"github.com/Comcast/wrp-go/wrp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/xmidt-org/svalinn/rules"
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
		encrypter             cipher.Encrypt
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
				encrypter: goodEncrypter,
				blacklist: goodBlacklist,
				inserter:  goodInserter,
				measures:  goodMeasures,
				config:    goodConfig,
				logger:    log.NewJSONLogger(os.Stdout),
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
				encrypter: goodEncrypter,
				blacklist: goodBlacklist,
				inserter:  goodInserter,
				measures:  goodMeasures,
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
			rp, err := NewRequestParser(tc.config, tc.logger, tc.registry, tc.inserter, tc.blacklist, tc.encrypter)
			if rp != nil {
				tc.expectedRequestParser.requestQueue = rp.requestQueue
				tc.expectedRequestParser.parseWorkers = rp.parseWorkers
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
	tests := []struct {
		description        string
		req                wrp.Message
		encryptErr         error
		expectEncryptCount float64
		expectParseCount   float64
	}{
		{
			description: "Success",
			req:         goodEvent,
		},
		{
			description: "Empty ID Error",
			req: wrp.Message{
				Destination: "//",
			},
			expectParseCount: 1.0,
		},
		{
			description:        "Encrypt Error",
			req:                goodEvent,
			encryptErr:         errors.New("encrypt failed"),
			expectEncryptCount: 1.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			encrypter := new(mockEncrypter)
			encrypter.On("EncryptMessage", mock.Anything).Return(tc.encryptErr)

			mblacklist := new(mockBlacklist)
			mblacklist.On("InList", mock.Anything).Return("", false).Once()

			mockInserter := new(mockInserter)
			mockInserter.On("Insert", mock.Anything).Return().Once()

			p := xmetricstest.NewProvider(nil, Metrics)
			m := NewMeasures(p)

			handler := RequestParser{
				encrypter: encrypter,
				config: Config{
					PayloadMaxSize:  9999,
					MetadataMaxSize: 9999,
					DefaultTTL:      time.Second,
					MaxWorkers:      5,
				},
				inserter:     mockInserter,
				parseWorkers: semaphore.New(2),
				measures:     m,
				logger:       logging.NewTestLogger(nil, t),
				blacklist:    mblacklist,
			}

			handler.parseWorkers.Acquire()
			handler.parseRequest(tc.req)
			mockInserter.AssertExpectations(t)
			mblacklist.AssertExpectations(t)
			encrypter.AssertExpectations(t)
			p.Assert(t, DroppedEventsCounter, reasonLabel, encryptFailReason)(xmetricstest.Value(tc.expectEncryptCount))
			p.Assert(t, DroppedEventsCounter, reasonLabel, parseFailReason)(xmetricstest.Value(tc.expectParseCount))

		})
	}
}

func TestCreateRecord(t *testing.T) {
	testassert := assert.New(t)
	goodTime, err := time.Parse(time.RFC3339Nano, "2019-02-13T21:19:02.614191735Z")
	testassert.Nil(err)
	tests := []struct {
		description      string
		req              wrp.Message
		storePayload     bool
		eventType        db.EventType
		inBlacklist      bool
		maxPayloadSize   int
		maxMetadataSize  int
		encryptErr       error
		expectedDeviceID string
		expectedEvent    wrp.Message
		emptyRecord      bool
		expectedReason   string
		expectedErr      error
	}{
		{
			description:      "Success",
			req:              goodEvent,
			expectedDeviceID: "test",
			expectedEvent:    goodEvent,
			storePayload:     true,
			eventType:        db.State,
			maxMetadataSize:  500,
			maxPayloadSize:   500,
		},
		{
			description: "Success Uppercase Device ID",
			req: wrp.Message{
				Source:          goodEvent.Source,
				Destination:     strings.ToUpper(goodEvent.Destination),
				PartnerIDs:      goodEvent.PartnerIDs,
				TransactionUUID: goodEvent.TransactionUUID,
				Type:            goodEvent.Type,
				Payload:         goodEvent.Payload,
				Metadata:        goodEvent.Metadata,
			},
			expectedDeviceID: "test",
			expectedEvent: wrp.Message{
				Source:          goodEvent.Source,
				Destination:     strings.ToUpper(goodEvent.Destination),
				PartnerIDs:      goodEvent.PartnerIDs,
				TransactionUUID: goodEvent.TransactionUUID,
				Type:            goodEvent.Type,
				Payload:         goodEvent.Payload,
				Metadata:        goodEvent.Metadata,
			},
			eventType:       db.State,
			storePayload:    true,
			maxMetadataSize: 500,
			maxPayloadSize:  500,
		},
		{
			description:      "Success Empty Metadata/Payload",
			req:              goodEvent,
			expectedDeviceID: "test",
			expectedEvent: wrp.Message{
				Source:          goodEvent.Source,
				Destination:     goodEvent.Destination,
				PartnerIDs:      goodEvent.PartnerIDs,
				TransactionUUID: goodEvent.TransactionUUID,
				Type:            goodEvent.Type,
				Payload:         nil,
				Metadata:        map[string]string{"error": "metadata provided exceeds size limit - too big to store"},
			},
			eventType: db.State,
		},
		{
			description: "Empty Dest ID Error",
			req: wrp.Message{
				Destination: "//",
			},
			eventType:      db.State,
			emptyRecord:    true,
			expectedReason: parseFailReason,
			expectedErr:    errEmptyID,
		},
		{
			description:    "Empty Source ID Error",
			req:            wrp.Message{},
			emptyRecord:    true,
			expectedReason: parseFailReason,
			expectedErr:    errEmptyID,
		},
		{
			description: "Blacklist Error",
			req: wrp.Message{
				Source: " ",
			},
			emptyRecord:    true,
			inBlacklist:    true,
			expectedReason: blackListReason,
			expectedErr:    errBlacklist,
		},
		{
			description: "Unexpected WRP Type Error",
			req: wrp.Message{
				Destination: "/device/",
				Type:        5,
			},
			eventType:      db.State,
			emptyRecord:    true,
			expectedReason: parseFailReason,
			expectedErr:    errUnexpectedWRPType,
		},
		{
			description:    "Encrypt Error",
			req:            goodEvent,
			encryptErr:     errors.New("encrypt failed"),
			emptyRecord:    true,
			expectedReason: encryptFailReason,
			expectedErr:    errors.New("failed to encrypt message"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			var buffer bytes.Buffer
			wrpEncoder := wrp.NewEncoder(&buffer, wrp.Msgpack)
			err := wrpEncoder.Encode(&tc.expectedEvent)
			assert.Nil(err)
			var expectedRecord db.Record
			if !tc.emptyRecord {
				expectedRecord = db.Record{
					Type:      tc.eventType,
					DeviceID:  tc.expectedDeviceID,
					BirthDate: goodTime.UnixNano(),
					DeathDate: goodTime.Add(time.Second).UnixNano(),
					Data:      buffer.Bytes(),
					Nonce:     []byte{},
					Alg:       string(cipher.None),
					KID:       "none",
				}
			}
			r, err := rules.NewRules([]rules.RuleConfig{
				{
					Regex:        ".*",
					StorePayload: tc.storePayload,
					RuleTTL:      time.Second,
				},
			})
			assert.Nil(err)
			rule, err := r.FindRule(" ")
			assert.Nil(err)
			encrypter := new(mockEncrypter)
			encrypter.On("EncryptMessage", mock.Anything).Return(tc.encryptErr)
			mblacklist := new(mockBlacklist)
			mblacklist.On("InList", mock.Anything).Return("", tc.inBlacklist).Once()
			handler := RequestParser{
				encrypter: encrypter,
				config: Config{
					PayloadMaxSize:  tc.maxPayloadSize,
					MetadataMaxSize: tc.maxMetadataSize,
				},
				blacklist: mblacklist,
			}
			record, reason, err := handler.createRecord(tc.req, rule, tc.eventType)
			encrypter.AssertExpectations(t)
			mblacklist.AssertExpectations(t)
			assert.Equal(expectedRecord, record)
			assert.Equal(tc.expectedReason, reason)
			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}
		})
	}
}

func TestGetBirthDate(t *testing.T) {
	testassert := assert.New(t)
	goodTime, err := time.Parse(time.RFC3339Nano, "2019-02-13T21:19:02.614191735Z")
	testassert.Nil(err)
	tests := []struct {
		description   string
		payload       []byte
		expectedTime  time.Time
		expectedFound bool
	}{
		{
			description:   "Success",
			payload:       goodEvent.Payload,
			expectedTime:  goodTime,
			expectedFound: true,
		},
		{
			description: "Unmarshal Payload Error",
			payload:     []byte("test"),
		},
		{
			description: "Empty Payload String Error",
			payload:     []byte(``),
		},
		{
			description: "Non-String Timestamp Error",
			payload:     []byte(`{"ts":5}`),
		},
		{
			description: "Parse Timestamp Error",
			payload:     []byte(`{"ts":"2345"}`),
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			time, found := getBirthDate(tc.payload)
			assert.Equal(time, tc.expectedTime)
			assert.Equal(found, tc.expectedFound)
		})
	}
}
