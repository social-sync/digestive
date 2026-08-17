BINARY := digestive

# UPDATE=1 rewrites the golden files in internal/inttest/golden with the
# observed round-trip output (review the diff before committing).
UPDATE_FLAG := $(if $(UPDATE),-args -update,)

.PHONY: build test test-integration vet fmt clean

build:
	CGO_ENABLED=0 go build -o $(BINARY) .

test:
	go test ./...

# Docker-backed, same-engine round-trip tests over every Laravel column type.
# Requires Docker. The SingleStore leg additionally needs a free license key in
# SINGLESTORE_LICENSE; without it that leg skips (MySQL still runs). See the
# README, "How the dialect tests are measured".
test-integration:
	go test -tags integration -count=1 -timeout 20m ./internal/inttest/ $(UPDATE_FLAG)

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BINARY)
	rm -rf exports
