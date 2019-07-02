# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.10.0]
- Store the birthdate and deathdate as Unix with Nanosecond precision.



## [v0.9.1]
- Added deathdate check
- Bumped codex



## [v0.9.0]
- Modified event parsing: if the eventType is state, parse the event 
  destination to find the device id.  Otherwise, take the event Source as the 
  device id.
- Added documentation in the form of updating the README and putting comments 
  in the yaml file.
- Refactored code to separate rules and requestParser into their own packages. 
  Also moved batchInserter to codex and refactored that.


## [v0.8.0]
- Added ability to turn off max batch size
- Bumped codex to v0.5.0
- Return 429 status code on full queue



## [v0.7.1]
 - close request body after reading it



## [v0.7.0]
 - Stopped building other services for integ tests
 - Added error check for making the request for getting the SAT
 - Added config defaults
 - If the birthdate is too far in the future, drop the event
 - Bumped `codex` and `bascule`
 - Added `wrp-go`
 - Store the wrp message as Msgpack



## [v0.6.1]
- fixed cipher yaml loading



## [v0.6.0]
- bumped codex to v0.4.0 for cipher upgrades



## [v0.5.1]
 - Create timestamp if it doesn't exist in payload
 - If request for acquiring SAT for webhook registration receives a non 200, return an error
 - Bumped codex-common to v0.3.3



## [v0.5.0]
- Added Blacklist
- Removed pruning
- Fixed shutdown order
- bumped codex to v0.3.2
- bumped webpa-common to v1.0.0


## [v0.4.0]
- adding basic level of encryption
- store event as wrp.Message



## [v0.3.0]
 - modified health metric to reflect unhealthy when pinging the database fails
 - device IDs are inserted into the db in lowercase



## [v0.2.7]
- replace dep with modules
- bumped codex




## [v0.2.6]
 - Bumped codex common
 - Converted times to Unix for the `db` package



## [v0.2.5]
 - Bumped codex version



## [v0.2.4]
 - Bumped codex



## [v0.2.3]
 - Limited number of goroutine workers running at one time
 - Enabled pprof
 - Increased file limit
 - Added metrics
 - Added batching insertion to database


## [v0.2.2]
Bug Fix: Webhook authorization config loading



## [v0.2.1]
Bug Fix Caduceus config loading




## [v0.2.0]
- added metrics
- fixed signature validation
- Created `webhook` package and created sat and basic token acquirers

- Added unit tests

## [v0.1.1]
- added health endpoint
- added bookkeeper for logging

## [v0.1.0]
- Initial creation
- Bumped codex version, modified code to match changes

[Unreleased]: https://github.com/Comcast/codex-svalinn/compare/v0.10.0...HEAD
[v0.10.0]: https://github.com/Comcast/codex-svalinn/compare/v0.9.1...v0.10.0
[v0.9.1]: https://github.com/Comcast/codex-svalinn/compare/v0.9.0...v0.9.1
[v0.9.0]: https://github.com/Comcast/codex-svalinn/compare/v0.8.0...v0.9.0
[v0.8.0]: https://github.com/Comcast/codex-svalinn/compare/v0.7.1...v0.8.0
[v0.7.1]: https://github.com/Comcast/codex-svalinn/compare/v0.7.0...v0.7.1
[v0.7.0]: https://github.com/Comcast/codex-svalinn/compare/v0.6.1...v0.7.0
[v0.6.1]: https://github.com/Comcast/codex-svalinn/compare/v0.6.0...v0.6.1
[v0.6.0]: https://github.com/Comcast/codex-svalinn/compare/v0.5.1...v0.6.0
[v0.5.1]: https://github.com/Comcast/codex-svalinn/compare/v0.5.0...v0.5.1
[v0.5.0]: https://github.com/Comcast/codex-svalinn/compare/v0.4.0...v0.5.0
[v0.4.0]: https://github.com/Comcast/codex-svalinn/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/Comcast/codex-svalinn/compare/v0.2.7...v0.3.0
[v0.2.7]: https://github.com/Comcast/codex-svalinn/compare/v0.2.6...v0.2.7
[v0.2.6]: https://github.com/Comcast/codex-svalinn/compare/v0.2.5...v0.2.6
[v0.2.5]: https://github.com/Comcast/codex-svalinn/compare/v0.2.4...v0.2.5
[v0.2.4]: https://github.com/Comcast/codex-svalinn/compare/v0.2.3...v0.2.4
[v0.2.3]: https://github.com/Comcast/codex-svalinn/compare/v0.2.2...v0.2.3
[v0.2.2]: https://github.com/Comcast/codex-svalinn/compare/v0.2.1...v0.2.2
[v0.2.1]: https://github.com/Comcast/codex-svalinn/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/Comcast/codex-svalinn/compare/v0.1.1...v0.2.0
[v0.1.1]: https://github.com/Comcast/codex-svalinn/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/Comcast/codex-svalinn/compare/0.0.0...v0.1.0
