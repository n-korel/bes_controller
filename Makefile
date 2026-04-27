.PHONY: build bucis bes clean lint test run-bucis run-bes run-bes-press opensips

build: bucis bes

./bin:
	mkdir -p "./bin"

bucis: ./bin
	go build -o "./bin/bucis" ./cmd/bucis

bes: ./bin
	go build -o "./bin/bes" ./cmd/bes

run-bucis: bucis
	-pkill -x bucis || true
	set -a; [ -f .env ] && . ./.env; set +a; "./bin/bucis"

run-bes: bes
	-pkill -x bes || true
	set -a; [ -f .env ] && . ./.env; set +a; "./bin/bes"

run-bes-press: bes
	-pkill -x bes || true
	set -a; [ -f .env ] && . ./.env; set +a; "./bin/bes" --press

clean:
	rm -rf "./bin"

lint:
	golangci-lint run ./...

test:
	go test ./...

opensips:
	-sudo pkill -f "opensips -f deploy/opensips/opensips.cfg"
	sudo opensips -f deploy/opensips/opensips.cfg
