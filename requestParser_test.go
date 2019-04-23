package main

import (
	"bytes"
	"errors"
	"github.com/Comcast/codex/cipher"
	"strings"
	"testing"
	"time"

	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/semaphore"
	"github.com/Comcast/webpa-common/xmetrics/xmetricstest"
	"github.com/Comcast/wrp-go/wrp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

func TestParseRequest(t *testing.T) {
	//require := require.New(t)
	//goodTime, err := time.Parse(time.RFC3339Nano, "2019-02-13T21:19:02.614191735Z")
	//require.NoError(err)
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

			p := xmetricstest.NewProvider(nil, Metrics)
			m := NewMeasures(p)

			handler := requestParser{
				//rules: []rule{},
				encrypter:       encrypter,
				payloadMaxSize:  9999,
				metadataMaxSize: 9999,
				defaultTTL:      time.Second,
				insertQueue:     make(chan db.Record, 10),
				maxParseWorkers: 5,
				parseWorkers:    semaphore.New(2),
				measures:        m,
				logger:          logging.NewTestLogger(nil, t),
				blacklist:       mblacklist,
			}

			handler.parseWorkers.Acquire()
			handler.parseRequest(tc.req)
			p.Assert(t, DroppedEventsCounter, reasonLabel, encryptFailReason)(xmetricstest.Value(tc.expectEncryptCount))
			p.Assert(t, DroppedEventsCounter, reasonLabel, parseFailReason)(xmetricstest.Value(tc.expectParseCount))
			p.Assert(t, DroppedEventsCounter, reasonLabel, dbFailReason)(xmetricstest.Value(0.0))

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
		},
		{
			description: "Empty ID Error",
			req: wrp.Message{
				Destination: "//",
			},
			emptyRecord:    true,
			expectedReason: parseFailReason,
			expectedErr:    errEmptyID,
		},
		{
			description: "Unexpected WRP Type Error",
			req: wrp.Message{
				Destination: "/device/",
				Type:        5,
			},
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
					Type:      0,
					DeviceID:  tc.expectedDeviceID,
					BirthDate: goodTime.Unix(),
					DeathDate: goodTime.Add(time.Second).Unix(),
					Data:      buffer.Bytes(),
					Nonce:     []byte{},
					Alg:       string(cipher.None),
					KID:       "none",
				}
			}
			rule := rule{
				storePayload: tc.storePayload,
				ttl:          time.Second,
			}
			encrypter := new(mockEncrypter)
			encrypter.On("EncryptMessage", mock.Anything).Return(tc.encryptErr)
			mblacklist := new(mockBlacklist)
			mblacklist.On("InList", mock.Anything).Return("", false).Once()
			handler := requestParser{
				encrypter:       encrypter,
				payloadMaxSize:  tc.maxPayloadSize,
				metadataMaxSize: tc.maxMetadataSize,
				blacklist:       mblacklist,
			}
			record, reason, err := handler.createRecord(tc.req, rule, 0)
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
