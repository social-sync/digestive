BINARY := digestive

.PHONY: build test vet fmt clean

build:
	CGO_ENABLED=0 go build -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BINARY)
	rm -rf exports
