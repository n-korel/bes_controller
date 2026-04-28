.PHONY: build bucis bes bes-windows clean lint test run-bucis run-bes run-bes-press opensips opensips-lan

build: bucis bes

./bin:
	mkdir -p "./bin"

bucis: ./bin
	go build -o "./bin/bucis" ./cmd/bucis

bes: ./bin
	go build -o "./bin/bes" ./cmd/bes

bes-windows: ./bin
	GOOS=windows GOARCH=amd64 go build -o "./bin/bes.exe" ./cmd/bes

run-bucis: bucis
	-pkill -x bucis || true
	set -a; [ -f .env ] && . ./.env; set +a; \
	p6710="$${EC_BUCIS_QUERY_PORT_6710:-6710}"; \
	p7777="$${EC_BUCIS_QUERY_PORT_7777:-7777}"; \
	for p in "$$p6710" "$$p7777"; do \
		i=0; \
		while [ $$i -lt 50 ]; do \
			python3 -c 'import socket,sys; p=int(sys.argv[1]); s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.bind(("0.0.0.0",p)); s.close()' "$$p" && break || true; \
			sleep 0.1; \
			i=$$((i+1)); \
		done; \
	done; \
	"./bin/bucis"

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

opensips-lan:
	-sudo pkill -f "opensips -f deploy/opensips/opensips.lan.cfg"
	sudo opensips -f deploy/opensips/opensips.lan.cfg
