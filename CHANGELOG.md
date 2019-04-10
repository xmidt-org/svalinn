# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/Comcast/codex-svalinn/compare/v0.4.0...HEAD
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
