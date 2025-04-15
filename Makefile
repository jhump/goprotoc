dev_build_version=$(shell git describe --tags --always --dirty)

.PHONY: ci
ci: deps checkgofmt errcheck golint vet ineffassign staticcheck test

.PHONY: deps
deps:
	go get -d -v -t ./...

.PHONY: updatedeps
updatedeps:
	go get -d -v -t -u -f ./...

.PHONY: install
install:
	go install -ldflags '-X "github.com/jhump/goprotoc/app/goprotoc.version=dev build $(dev_build_version)"' ./...

.PHONY: release
release:
	@go install github.com/goreleaser/goreleaserv0.134.0
	goreleaser --rm-dist

.PHONY: checkgofmt
checkgofmt:
	@echo gofmt -s -l .
	@if [ -n "$$(go version | awk '{ print $$3 }' | grep -v devel)" ]; then \
		output="$$(gofmt -s -l .)" ; \
		if [ -n "$$output"  ]; then \
		    echo "$$output"; \
			echo "Run gofmt on the above files!"; \
			exit 1; \
		fi; \
	fi

.PHONY: vet
vet:
	go vet ./...

.PHONY: staticcheck
staticcheck:
	@go install honnef.co/go/tools/cmd/staticcheck@v0.4.6
	staticcheck ./...

.PHONY: ineffassign
ineffassign:
	@go install github.com/gordonklaus/ineffassign@v0.0.0-20200309095847-7953dde2c7bf
	ineffassign .

.PHONY: golint
golint:
	@go install golang.org/x/lint/golint@v0.0.0-20210508222113-6edffad5e616
	golint -min_confidence 0.9 -set_exit_status ./...

.PHONY: errcheck
errcheck:
	@go install github.com/kisielk/errcheck@v1.6.3
	errcheck ./...

.PHONY: test
test:
	go test -cover -race ./...

.PHONY: generate
generate:
	go generate ./...

.PHONY: testcover
testcover:
	@echo go test -race -covermode=atomic ./...
	@echo "mode: atomic" > coverage.out
	@for dir in $$(go list ./...); do \
		go test -race -coverprofile profile.out -covermode=atomic $$dir ; \
		if [ -f profile.out ]; then \
			tail -n +2 profile.out >> coverage.out && rm profile.out ; \
		fi \
	done

