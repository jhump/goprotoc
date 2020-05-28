dev_build_version=$(shell git describe --tags --always --dirty)

.PHONY: ci
ci: deps checkgofmt vet predeclared staticcheck ineffassign errcheck golint golint test

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
	@GO111MODULE=off go get github.com/goreleaser/goreleaser
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
	@go get honnef.co/go/tools/cmd/staticcheck
	staticcheck ./...

.PHONY: ineffassign
ineffassign:
	@go get github.com/gordonklaus/ineffassign
	ineffassign .

.PHONY: predeclared
predeclared:
	@go get github.com/nishanths/predeclared
	predeclared ./...

.PHONY: golint
golint:
	@go get golang.org/x/lint/golint
	golint -min_confidence 0.9 -set_exit_status ./...

.PHONY: errcheck
errcheck:
	@go get github.com/kisielk/errcheck
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

