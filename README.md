# Svalinn
(pronounced “svæ-lin”)

[![Build Status](https://travis-ci.com/Comcast/codex-svalinn.svg?branch=master)](https://travis-ci.com/Comcast/codex-svalinn)
[![codecov.io](http://codecov.io/github/Comcast/codex-svalinn/coverage.svg?branch=master)](http://codecov.io/github/Comcast/codex-svalinn?branch=master)
[![Code Climate](https://codeclimate.com/github/Comcast/codex-svalinn/badges/gpa.svg)](https://codeclimate.com/github/Comcast/codex-svalinn)
[![Issue Count](https://codeclimate.com/github/Comcast/codex-svalinn/badges/issue_count.svg)](https://codeclimate.com/github/Comcast/codex-svalinn)
[![Go Report Card](https://goreportcard.com/badge/github.com/xmidt-org/svalinn)](https://goreportcard.com/report/github.com/xmidt-org/svalinn)
[![Apache V2 License](http://img.shields.io/badge/license-Apache%20V2-blue.svg)](https://github.com/xmidt-org/svalinn/blob/master/LICENSE)
[![GitHub release](https://img.shields.io/github/release/Comcast/codex-svalinn.svg)](CHANGELOG.md)


## Summary

Svalinn has an endpoint that receives events as [WRP Messages](https://github.com/Comcast/wrp-c/wiki/Web-Routing-Protocol).
The service parses these events and inserts them into the database. It also 
optionally registers to an endpoint to receive events.  For more information on
how Svalinn fits into codex, check out [the codex README](https://github.com/Comcast/codex).

For registering to an endpoint, Svalinn is capable of registering to [Caduceus](https://github.com/Comcast/caduceus),
a part of [XMiDT]((https://github.com/Comcast/xmidt)).

## Details

Svalinn has two main functions: registering for events and inserting events 
into the database.  It also has a health endpoint that reports as unhealthy 
when it can't connect to or ping the database.

[MsgPack](https://msgpack.org/index.html) is used multiple time in Svalinn, 
specifically [ugorji's implementation](https://github.com/ugorji/go).

### Registering for events

Whether or not Svalinn registers to a webhook for events is determined by the 
configurable webhook registration interval.  So long as the interval is greater 
than 0, the registerer will register at the interval given.

When the registerer contacts the webhook, it includes the following information:
* **URL:** where to send the events
* **Content type:** what to set as the message's content type and how to send 
  events to Svalinn.  Svalinn requests for a `wrp`, which will come as a 
  `MsgPack`.
* **Secret:** the secret the webhook should use when sending events to Svalinn.
  Svalinn uses it to validate the message.
* **Device IDs:** list of regular expressions to match device id type against.
  Currently, Svalinn sets this as `[".*"]`
* **Events:** list of regular expressions for the webhook to use to determine 
  which events to send to Svalinn.

The registerer sends an Authorization header with its request, and determines 
what that should be based on configuration.

### Inserting events into the database

When an event is sent to Svalinn's endpoint, it is initially [validated](#Validation),
[parsed](#Parsing-(and-Encryption)) into a record to be stored in the database, 
then inserted as part of a [batch insert](#Batch-Insertion) into the database.

#### Validation

In order to ensure that the event was sent from a trusted source, Svalinn 
gets a SHA1 hash from the `X-Webpa-Signature` Header, then creates its own hash 
using the secret that it sends when registering and the body of the request.  
If the two hashes match, the event is considered valid and is decoded from 
`MsgPack` into the `wrp.Message` struct: our event!

Now that the event has been verified and decoded, Svalinn attempts to add it to 
the parsing queue.  If the queue is full, Svalinn returns the `Too Many Requests`
(429) status code, drops the message, and records it as dropped in metrics.  
Otherwise, Svalinn adds the event to the queue and returns the `Accepted` (202) 
status code.

#### Parsing (and Encryption)

A goroutine watches the parsing queue and spawns other goroutines to parse the 
events it finds on the queue into the records we will store in the database.  
There is a configurable maximum number of workers to parse the incoming events.

A worker parsing a request runs the following steps:

1. Checks to see if there are any rules relating to this event's `Destination`.
   If a rule's regular expression matches, the rule provides guidance on what 
   the event type of the event's record should be, the TTL for the record, and 
   whether or not to store the payload of the event.  Rules are declared in 
   Svalinn's configuration.
2. Parses the event's `Destination` to determine the `device id`, which is added 
   to the record we are going to store.
3. Determines if the record is in the blacklist.
4. Checks that the `Type` is what we expect.
5. Gets a timestamp from the `Payload` for the record's `birth date`.  If a 
   timestamp isn't found, Svalinn creates a new one from the current time.
   The worker also takes the time and adds the TTL for the record in order 
   to find the `death date`, which is when the record has expired and should 
   be deleted.  If a TTL isn't decided by a rule, the (configurable) default 
   TTL is used.  Both the `birth date` and `death date` are added to the record.
6. Determines if the event's `Payload` and `Metadata` should be stored.  The 
   `Payload` is not stored by default unless it is part of a rule enabling the 
   storage of the `Payload`.  However, if its size is bigger than the configured 
   max size allowed, it isn't stored.  The `Metadata` is stored by default 
   unless it is larger than the configured max size allowed.  If the 
   `Payload` or `Metadata` shouldn't be stored, they are stripped from the 
   event.
7. The event (possibly without `Metadata` and `Payload`) is encoded into a 
   `MsgPack`.
8. If an encryption has been set up, we encrypt the encoded event and add 
   it to the record we plan to insert into the database.  If there is no 
   encryption, the encoded event is added to the record.

If any of these steps fail, the worker drops the message, records the drop and 
the reason in metrics, and then finishes.

At this point, if the worker succeeded, the record has the following 
information:
* `Type`
* `DeviceID`
* `BirthDate`
* `DeathDate`
* `Data` (the encoded/encrypted event)
* `Nonce` (for the encryption)
* `Alg` (for the encryption)
* `KID` (for the encryption)

The worker adds the record to the inserting queue, blocking until it succeeds.
Once it succeeds, it finishes so a new goroutine can parse a new event.

#### Batch Insertion

A goroutine watches the inserting queue.  When it finds a record, a timer 
starts while the goroutine waits for more records.  If it reaches the 
configurable maximum batch size, it spawns a worker to batch insert that group 
of records.  The timer that started was the maximum time the goroutine will 
wait until spawning a goroutine to batch insert the records it has gathered.
When the timer goes off, the goroutine spawns a worker to batch insert its 
records, even though it didn't hit the max batch size yet.  The number of 
maximum inserting workers at a time is a configurable value.  If there are 
no workers available, the goroutine will block until it is able to spawn a new 
worker to batch insert the records it has.

The spawned worker will attempt to insert the records.  Depending on the 
configuration, it may retry a set number of times.  If it ultimately fails, it 
will count the number of records in the batch and record that number of dropped 
events in metrics.  When the worker is done, it finishes so a new goroutine 
can do a new insertion.

## Build

### Source

In order to build from the source, you need a working Go environment with 
version 1.11 or greater. Find more information on the [Go website](https://golang.org/doc/install).

You can directly use `go get` to put the Svalinn binary into your `GOPATH`:
```bash
GO111MODULE=on go get github.com/xmidt-org/svalinn
```

You can also clone the repository yourself and build using make:

```bash
mkdir -p $GOPATH/src/github.com/Comcast
cd $GOPATH/src/github.com/Comcast
git clone git@github.com:Comcast/codex-svalinn.git
cd codex-svalinn
make build
```

### Makefile

The Makefile has the following options you may find helpful:
* `make build`: builds the Svalinn binary
* `make rpm`: builds an rpm containing Svalinn
* `make docker`: builds a docker image for Svalinn, making sure to get all 
   dependencies
* `make local-docker`: builds a docker image for Svalinn with the assumption
   that the dependencies can be found already
* `make it`: runs `make docker`, then deploys Svalinn and a cockroachdb 
   database into docker.
* `make test`: runs unit tests with coverage for Svalinn
* `make clean`: deletes previously-built binaries and object files

### Docker

The docker image can be built either with the Makefile or by running a docker 
command.  Either option requires first getting the source code.

See [Makefile](#Makefile) on specifics of how to build the image that way.

For running a command, either you can run `docker build` after getting all 
dependencies, or make the command fetch the dependencies.  If you don't want to 
get the dependencies, run the following command:
```bash
docker build -t svalinn:local -f deploy/Dockerfile .
```
If you want to get the dependencies then build, run the following commands:
```bash
GO111MODULE=on go mod vendor
docker build -t svalinn:local -f deploy/Dockerfile.local .
```

For either command, if you want the tag to be a version instead of `local`, 
then replace `local` in the `docker build` command.

### Kubernetes

WIP. TODO: add info

## Deploy

For deploying on Docker or in Kubernetes, refer to the [deploy README](https://github.com/Comcast/codex/tree/master/deploy/README.md).

For running locally, ensure you have the binary [built](#Source).  If it's in 
your `GOPATH`, run:
```
svalinn
```
If the binary is in your current folder, run:
```
./svalinn
```

## Contributing

Refer to [CONTRIBUTING.md](CONTRIBUTING.md).
