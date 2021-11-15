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
	timeFunc := func() time.Time {
		return goodTime
	}
	tests := []struct {
		description      string
		req              wrp.Message
		storePayload     bool
		blacklistCalled  bool
		inBlacklist      bool
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
			blacklistCalled:  true,
			encryptCalled:    true,
			maxMetadataSize:  500,
			maxPayloadSize:   500,
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
			encryptCalled:   true,
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
			emptyRecord:     true,
			blacklistCalled: true,
			expectedReason:  parseFailReason,
			expectedErr:     errUnexpectedWRPType,
		},
		{
			description:     "Encrypt Error",
			req:             goodEvent,
			encryptErr:      errors.New("encrypt failed"),
			emptyRecord:     true,
			blacklistCalled: true,
			encryptCalled:   true,
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
					Type:      db.State,
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

			handler := RequestParser{
				rc: RecordConfig{
					encrypter: encrypter,
					blacklist: mblacklist,
					currTime:  timeFunc,
				},

				config: Config{
					PayloadMaxSize:  tc.maxPayloadSize,
					MetadataMaxSize: tc.maxMetadataSize,
				},
			}
			record, reason, err := handler.createRecord(tc.req, rule, db.State)
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

func TestParseDeviceID(t *testing.T) {
	tests := []struct {
		description string
		eventType   db.EventType
		dest        string
		src         string
		expectedID  string
		expectedErr error
	}{
		{
			description: "Success",
			eventType:   db.State,
			dest:        goodEvent.Destination,
			src:         goodEvent.Source,
			expectedID:  "test",
			expectedErr: nil,
		},
		{
			description: "Success Uppercase Device ID",
			eventType:   db.State,
			dest:        strings.ToUpper(goodEvent.Destination),
			src:         goodEvent.Source,
			expectedID:  "test",
			expectedErr: nil,
		},
		{
			description: "Success Source Device",
			dest:        goodEvent.Destination,
			src:         goodEvent.Source,
			expectedID:  goodEvent.Source,
			expectedErr: nil,
		},
		{
			description: "Empty Dest ID Error",
			eventType:   db.State,
			dest:        "//",
			src:         goodEvent.Source,
			expectedID:  "",
			expectedErr: errEmptyID,
		},
		{
			description: "Empty Source ID Error",
			dest:        goodEvent.Destination,
			src:         "",
			expectedID:  "",
			expectedErr: errEmptyID,
		},
	}
	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			msg := wrp.Message{
				Source:      tc.src,
				Destination: tc.dest,
			}
			id, err := parseDeviceID(tc.eventType, msg)
			assert.Equal(tc.expectedID, id)
			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}
		})
	}
}

func TestGetValidBirthDeathDates(t *testing.T) {
	testassert := assert.New(t)
	goodTime, err := time.Parse(time.RFC3339Nano, "2019-02-13T21:19:02.614191735Z")
	testassert.Nil(err)
	currTime, err := time.Parse(time.RFC3339Nano, "2019-02-13T21:21:21.614191735Z")
	testassert.Nil(err)

	r, err := rules.NewRules([]rules.RuleConfig{
		{
			Regex:        ".*",
			StorePayload: true,
			RuleTTL:      2 * time.Hour,
		},
	})
	testassert.Nil(err)
	rule, err := r.FindRule(" ")
	testassert.Nil(err)

	tests := []struct {
		description       string
		fakeNow           time.Time
		payload           []byte
		rule              *rules.Rule
		expectedBirthDate int64
		expectedDeathDate int64
		expectedReason    string
		expectedErr       error
	}{
		{
			description:       "Success",
			fakeNow:           currTime,
			payload:           goodEvent.Payload,
			expectedBirthDate: goodTime.UnixNano(),
			expectedDeathDate: goodTime.Add(time.Hour).UnixNano(),
		},
		{
			description:       "Success No Birthdate in Payload",
			fakeNow:           currTime,
			payload:           nil,
			expectedBirthDate: currTime.UnixNano(),
			expectedDeathDate: currTime.Add(time.Hour).UnixNano(),
		},
		{
			description:       "Success with Rule",
			fakeNow:           goodTime,
			payload:           goodEvent.Payload,
			rule:              rule,
			expectedBirthDate: goodTime.UnixNano(),
			expectedDeathDate: goodTime.Add(2 * time.Hour).UnixNano(),
		},
		{
			description:    "Future Birthdate Error",
			fakeNow:        currTime.Add(-5 * time.Hour),
			payload:        goodEvent.Payload,
			expectedReason: invalidBirthdateReason,
			expectedErr:    errFutureBirthdate,
		},
		{
			description:    "Past Deathdate Error",
			fakeNow:        currTime.Add(5 * time.Hour),
			payload:        goodEvent.Payload,
			expectedReason: expiredReason,
			expectedErr:    errExpired,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			currTime := func() time.Time {
				return tc.fakeNow
			}
			b, d, reason, err := getValidBirthDeathDates(currTime, tc.payload, tc.rule, time.Hour)
			assert.Equal(tc.expectedBirthDate, b, "birth date mismatch")
			assert.Equal(tc.expectedDeathDate, d, "death date mismatch")
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
