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
	"bytes"
	"encoding/json"
	"path"
	"strings"
	"time"

	"github.com/goph/emperror"
	db "github.com/xmidt-org/codex-db"
	"github.com/xmidt-org/svalinn/rules"
	"github.com/xmidt-org/wrp-go/v2"
)

func (r *RequestParser) createRecord(req wrp.Message, rule *rules.Rule, eventType db.EventType) (db.Record, string, error) {
	var (
		err         error
		emptyRecord db.Record
		record      = db.Record{Type: eventType}
	)

	record.DeviceID, err = parseDeviceID(eventType, req)
	if err != nil {
		return emptyRecord, parseFailReason, err
	}

	if reason, ok := r.blacklist.InList(record.DeviceID); ok {
		return emptyRecord, blackListReason, emperror.With(errBlacklist, "reason", reason)
	}

	// verify wrp is the right type
	if req.Type != wrp.SimpleEventMessageType {
		return emptyRecord, parseFailReason, emperror.WrapWith(errUnexpectedWRPType, "message type check failed", "type", req.Type, "full message", req)
	}

	msg := req
	var reason string
	record.BirthDate, record.DeathDate, reason, err = getValidBirthDeathDates(r.currTime, msg.Payload, rule, r.config.DefaultTTL)
	if err != nil {
		return emptyRecord, reason, err
	}

	// store the payload if we are supposed to and it's not too big
	storePayload := (rule != nil && rule.StorePayload()) || false
	if !storePayload || len(msg.Payload) > r.config.PayloadMaxSize {
		msg.Payload = nil
	}

	// if metadata is too large, store a message explaining that instead of the metadata
	marshaledMetadata, err := json.Marshal(msg.Metadata)
	if err != nil {
		return emptyRecord, parseFailReason, emperror.WrapWith(err, "failed to marshal metadata to determine size", "metadata", msg.Metadata, "full message", req)
	}
	if len(marshaledMetadata) > r.config.MetadataMaxSize {
		msg.Metadata = make(map[string]string)
		msg.Metadata["error"] = "metadata provided exceeds size limit - too big to store"
	}

	var buffer bytes.Buffer
	msgEncoder := wrp.NewEncoder(&buffer, wrp.Msgpack)
	err = msgEncoder.Encode(&msg)
	if err != nil {
		return emptyRecord, marshalFailReason, emperror.WrapWith(err, "failed to marshal event", "full message", req)
	}

	encyptedData, nonce, err := r.encrypter.EncryptMessage(buffer.Bytes())
	if err != nil {
		return emptyRecord, encryptFailReason, emperror.WrapWith(err, "failed to encrypt message")
	}
	record.Data = encyptedData
	record.Nonce = nonce
	record.Alg = string(r.encrypter.GetAlgorithm())
	record.KID = r.encrypter.GetKID()

	return record, "", nil
}

func parseDeviceID(eventType db.EventType, req wrp.Message) (string, error) {
	if eventType == db.State {
		// get state and id from dest if this is a state event
		base, _ := path.Split(req.Destination)
		_, deviceId := path.Split(path.Base(base))
		if deviceId == "" {
			return "", emperror.WrapWith(errEmptyID, "id check failed", "request destination", req.Destination, "full message", req)
		}
		return strings.ToLower(deviceId), nil
	}

	if req.Source == "" {
		return "", emperror.WrapWith(errEmptyID, "id check failed", "request Source", req.Source, "full message", req)
	}
	return strings.ToLower(req.Source), nil
}

func getValidBirthDeathDates(currTime func() time.Time, payload []byte, rule *rules.Rule, defaultTTL time.Duration) (int64, int64, string, error) {
	now := currTime()
	birthDate, ok := getBirthDate(payload)
	if !ok {
		birthDate = now
	}
	if birthDate.After(now.Add(time.Hour)) {
		return 0, 0, invalidBirthdateReason, emperror.WrapWith(errFutureBirthdate, "invalid birthdate", "birthdate", birthDate.String())
	}
	ttl := defaultTTL
	if rule != nil && rule.TTL() != 0 {
		ttl = rule.TTL()
	}
	deathDate := birthDate.Add(ttl)
	if now.After(deathDate) {
		return 0, 0, expiredReason, emperror.WrapWith(errExpired, "event is already expired", "deathdate", deathDate.String())
	}
	return birthDate.UnixNano(), deathDate.UnixNano(), "", nil
}

func getBirthDate(payload []byte) (time.Time, bool) {
	p := make(map[string]interface{})
	if len(payload) == 0 {
		return time.Time{}, false
	}
	err := json.Unmarshal(payload, &p)
	if err != nil {
		return time.Time{}, false
	}

	// parse the time from the payload
	timeString, ok := p["ts"].(string)
	if !ok {
		return time.Time{}, false
	}
	birthDate, err := time.Parse(time.RFC3339Nano, timeString)
	if err != nil {
		return time.Time{}, false
	}
	return birthDate, true
}
