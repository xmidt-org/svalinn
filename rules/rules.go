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
	"time"

	"github.com/goph/emperror"
)

var (
	errNoMatch = errors.New("No key matches this destination")
)

type RuleConfig struct {
	Regex        string
	StorePayload bool
	RuleTTL      time.Duration
	EventType    string
}

type Rule struct {
	regex        *regexp.Regexp
	storePayload bool
	ttl          time.Duration
	eventType    string
}

type Rules []*Rule

func NewRules(rules []RuleConfig) (Rules, error) {
	parsedRules := Rules(make([]*Rule, len(rules)))
	for _, r := range rules {
		regex, err := regexp.Compile(r.Regex)
		if err != nil {
			return parsedRules, emperror.WrapWith(err, "Failed to compile regexp rule", "regexp attempted", r.Regex)
		}
		parsedRules = append(parsedRules, &Rule{regex, r.StorePayload, r.RuleTTL, r.EventType})
	}
	return parsedRules, nil
}

func (r Rules) FindRule(dest string) (*Rule, error) {
	for _, rule := range r {
		if rule.regex.MatchString(dest) {
			return rule, nil
		}
	}
	return nil, errNoMatch
}

func (r *Rule) EventType() string {
	return r.eventType
}

func (r *Rule) StorePayload() bool {
	return r.storePayload
}

func (r *Rule) TTL() time.Duration {
	return r.ttl
}
