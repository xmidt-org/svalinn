DEFAULT: build

GO           ?= go
GOFMT        ?= $(GO)fmt
FIRST_GOPATH := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
SVALINN    := $(FIRST_GOPATH)/bin/svalinn

PROGVER = $(shell git describe --tags `git rev-list --tags --max-count=1` | tail -1 | sed 's/v\(.*\)/\1/')

.PHONY: go-mod-vendor
go-mod-vendor:
	GO111MODULE=on $(GO) mod vendor

.PHONY: build
build: go-mod-vendor
	$(GO) build -o svalinn

rpm:
	mkdir -p ./.ignore/sources
	tar -czvf ./.ignore/sources/svalinn-$(PROGVER).tar.gz . --exclude ./.git --exclude ./OPATH --exclude ./conf --exclude ./deploy --exclude ./vendor
	cp conf/svalinn.service ./.ignore/sources/
	cp conf/svalinn.yaml  ./.ignore/sources/
	cp LICENSE ./.ignore/sources/
	cp NOTICE ./.ignore/sources/
	cp CHANGELOG.md ./.ignore/sources/
	rpmbuild --define "_topdir $(CURDIR)/OPATH" \
    		--define "_version $(PROGVER)" \
    		--define "_release 1" \
    		-ba deploy/packaging/svalinn.spec

.PHONY: version
version:
	@echo $(PROGVER)

# If the first argument is "update-version"...
ifeq (update-version,$(firstword $(MAKECMDGOALS)))
  # use the rest as arguments for "update-version"
  RUN_ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
  # ...and turn them into do-nothing targets
  $(eval $(RUN_ARGS):;@:)
endif

.PHONY: update-version
update-version:
	@echo "Update Version $(PROGVER) to $(RUN_ARGS)"
	git tag v$(RUN_ARGS)

.PHONY: install
install: go-mod-vendor
	go install -ldflags "-X 'main.BuildTime=`date -u '+%Y-%m-%d %H:%M:%S'`' -X main.GitCommit=`git rev-parse --short HEAD` -X main.Version=$(PROGVER)"

.PHONY: release-artifacts
release-artifacts: go-mod-vendor
	GOOS=darwin GOARCH=amd64 go build  -ldflags "-X 'main.BuildTime=`date -u '+%Y-%m-%d %H:%M:%S'`' -X main.GitCommit=`git rev-parse --short HEAD` -X main.Version=$(PROGVER)" -o ./.ignore/svalinn-$(PROGVER).darwin-amd64
	GOOS=linux  GOARCH=amd64 go build  -ldflags "-X 'main.BuildTime=`date -u '+%Y-%m-%d %H:%M:%S'`' -X main.GitCommit=`git rev-parse --short HEAD` -X main.Version=$(PROGVER)" -o ./.ignore/svalinn-$(PROGVER).linux-amd64

.PHONY: docker
docker:
	docker build --build-arg VERSION=$(PROGVER) -f ./deploy/Dockerfile -t xmidt/svalinn:$(PROGVER) .

# build docker without running modules
.PHONY: local-docker
local-docker:
	GOOS=linux  GOARCH=amd64 go build -o svalinn_linux_amd64
	docker build --build-arg VERSION=$(PROGVER)+local -f ./deploy/Dockerfile.local -t xmidt/svalinn:local .

.PHONY: style
style:
	! gofmt -d $$(find . -path ./vendor -prune -o -name '*.go' -print) | grep '^'

.PHONY: test
test: go-mod-vendor
	GO111MODULE=on go test -v -race  -coverprofile=cover.out ./...

.PHONY: test-cover
test-cover: test
	go tool cover -html=cover.out

.PHONY: codecov
codecov: test
	curl -s https://codecov.io/bash | bash

.PHONEY: it
it:
	./it.sh

.PHONY: clean
clean:
	rm -rf ./codex-svalinn ./OPATH ./coverage.txt ./vendor
