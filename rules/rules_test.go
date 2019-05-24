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

package rules

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewRules(t *testing.T) {
	tests := []struct {
		description    string
		rules          []RuleConfig
		expectedOutput Rules
		expectedErr    error
	}{
		{
			description: "No Rules Success",
		},
		{
			description: "Success With Rules",
			rules: []RuleConfig{
				{
					Regex:        ".*",
					StorePayload: true,
					RuleTTL:      time.Duration(5) * time.Second,
					EventType:    "test event",
				},
			},
			expectedOutput: []*Rule{
				&Rule{
					regex:        regexp.MustCompile(".*"),
					storePayload: true,
					ttl:          time.Duration(5) * time.Second,
					eventType:    "test event",
				},
			},
			expectedErr: nil,
		},
		{
			description: "Parse Error",
			rules: []RuleConfig{
				{
					Regex: "((((((((",
				},
			},
			expectedErr: errors.New("Failed to compile regexp rule"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			rules, err := NewRules(tc.rules)
			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}
			assert.Equal(tc.expectedOutput, rules)
		})
	}
}

func TestFindRule(t *testing.T) {
	goodRule := &Rule{
		regex:        regexp.MustCompile(".*ccc$"),
		storePayload: false,
		ttl:          time.Duration(3) * time.Minute,
		eventType:    "test event",
	}
	tests := []struct {
		description  string
		rules        Rules
		dest         string
		expectedRule *Rule
		expectedErr  error
	}{
		{
			description:  "Success",
			rules:        []*Rule{goodRule},
			dest:         "aaa/bbb/ccc",
			expectedRule: goodRule,
			expectedErr:  nil,
		},
		{
			description: "No Match Error",
			dest:        "ab/cd/ef",
			expectedErr: errNoMatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			rule, err := tc.rules.FindRule(tc.dest)
			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}
			assert.Equal(tc.expectedRule, rule)
			if rule != nil && tc.expectedRule != nil {
				assert.Equal(tc.expectedRule.eventType, rule.EventType())
				assert.Equal(tc.expectedRule.storePayload, rule.StorePayload())
				assert.Equal(tc.expectedRule.ttl, rule.TTL())
			}
		})
	}
}
