package requestParser

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	db "github.com/xmidt-org/codex-db"
	"github.com/xmidt-org/svalinn/rules"
	"github.com/xmidt-org/voynicrypto"
	"github.com/xmidt-org/wrp-go/v2"
)

func TestCreateRecord(t *testing.T) {
	testassert := assert.New(t)
	goodTime, err := time.Parse(time.RFC3339Nano, "2019-02-13T21:19:02.614191735Z")
	testassert.Nil(err)
	tests := []struct {
		description      string
		req              wrp.Message
		storePayload     bool
		eventType        db.EventType
		blacklistCalled  bool
		inBlacklist      bool
		timeExpected     bool
		timeToReturn     time.Time
		maxPayloadSize   int
		maxMetadataSize  int
		encryptCalled    bool
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
			blacklistCalled:  true,
			timeExpected:     true,
			timeToReturn:     goodTime,
			encryptCalled:    true,
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
			blacklistCalled: true,
			timeExpected:    true,
			timeToReturn:    goodTime,
			encryptCalled:   true,
			maxMetadataSize: 500,
			maxPayloadSize:  500,
		},
		{
			description: "Success Source Device and No Birthdate",
			req: wrp.Message{
				Source:          goodEvent.Source,
				PartnerIDs:      goodEvent.PartnerIDs,
				TransactionUUID: goodEvent.TransactionUUID,
				Type:            goodEvent.Type,
				Metadata:        goodEvent.Metadata,
			},
			expectedDeviceID: goodEvent.Source,
			expectedEvent: wrp.Message{
				Source:          goodEvent.Source,
				PartnerIDs:      goodEvent.PartnerIDs,
				TransactionUUID: goodEvent.TransactionUUID,
				Type:            goodEvent.Type,
				Metadata:        goodEvent.Metadata,
			},
			storePayload:    true,
			blacklistCalled: true,
			timeExpected:    true,
			timeToReturn:    goodTime,
			encryptCalled:   true,
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
			blacklistCalled: true,
			timeExpected:    true,
			timeToReturn:    goodTime,
			encryptCalled:   true,
			eventType:       db.State,
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
			emptyRecord:     true,
			inBlacklist:     true,
			blacklistCalled: true,
			expectedReason:  blackListReason,
			expectedErr:     errBlacklist,
		},
		{
			description: "Unexpected WRP Type Error",
			req: wrp.Message{
				Destination: "/device/",
				Type:        5,
			},
			eventType:       db.State,
			emptyRecord:     true,
			blacklistCalled: true,
			expectedReason:  parseFailReason,
			expectedErr:     errUnexpectedWRPType,
		},
		{
			description:     "Future Birthdate Error",
			req:             goodEvent,
			eventType:       db.State,
			emptyRecord:     true,
			blacklistCalled: true,
			timeExpected:    true,
			timeToReturn:    goodTime.Add(-5 * time.Hour),
			expectedReason:  invalidBirthdateReason,
			expectedErr:     errFutureBirthdate,
		},
		{
			description:     "Past Deathdate Error",
			req:             goodEvent,
			eventType:       db.State,
			emptyRecord:     true,
			blacklistCalled: true,
			timeExpected:    true,
			timeToReturn:    goodTime.Add(5 * time.Hour),
			expectedReason:  expiredReason,
			expectedErr:     errExpired,
		},
		{
			description:     "Encrypt Error",
			req:             goodEvent,
			encryptErr:      errors.New("encrypt failed"),
			emptyRecord:     true,
			blacklistCalled: true,
			encryptCalled:   true,
			timeExpected:    true,
			timeToReturn:    goodTime,
			expectedReason:  encryptFailReason,
			expectedErr:     errors.New("failed to encrypt message"),
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
					Alg:       string(voynicrypto.None),
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
			if tc.encryptCalled {
				encrypter.On("EncryptMessage", mock.Anything).Return(tc.encryptErr).Once()
			}
			mblacklist := new(mockBlacklist)
			if tc.blacklistCalled {
				mblacklist.On("InList", mock.Anything).Return("", tc.inBlacklist).Once()
			}

			timeCalled := false
			timeFunc := func() time.Time {
				timeCalled = true
				return tc.timeToReturn
			}

			handler := RequestParser{
				encrypter: encrypter,
				config: Config{
					PayloadMaxSize:  tc.maxPayloadSize,
					MetadataMaxSize: tc.maxMetadataSize,
				},
				blacklist: mblacklist,
				currTime:  timeFunc,
			}
			record, reason, err := handler.createRecord(tc.req, rule, tc.eventType)
			encrypter.AssertExpectations(t)
			mblacklist.AssertExpectations(t)
			assert.Equal(expectedRecord, record)
			assert.Equal(tc.expectedReason, reason)
			assert.Equal(tc.timeExpected, timeCalled)
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
