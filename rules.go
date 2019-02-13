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
	"errors"
	"regexp"
	"time"

	"github.com/goph/emperror"
)

type rule struct {
	regex        *regexp.Regexp
	key          string
	storePayload bool
	ttl          time.Duration
	eventType    string
}

func createRules(rules []RuleConfig) ([]rule, error) {
	var parsedRules []rule
	for _, r := range rules {
		regex, err := regexp.Compile(r.Regex)
		if err != nil {
			return parsedRules, emperror.WrapWith(err, "Failed to Compile regexp rule", "key", r.TombstoneKey, "regexp attempted", r.Regex)
		}
		parsedRules = append(parsedRules, rule{regex, r.TombstoneKey, r.StorePayload, r.RuleTTL, r.EventType})
	}
	return parsedRules, nil
}

func findRule(rules []rule, dest string) (rule, error) {
	for _, r := range rules {
		if r.regex.MatchString(dest) {
			return r, nil
		}
	}
	return rule{}, errors.New("No key matches this destination")
}
