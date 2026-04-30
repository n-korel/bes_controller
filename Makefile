.PHONY: all build clean \
	bin \
	bucis bes bes-windows \
	fmt fmt-check \
	lint vet \
	test test-race-cover bench \
	vulncheck tidy \
	check ci \
	run-bucis run-bes run-bes-press \
	opensips opensips-lan

all: build

bin:
	mkdir -p ./bin

build: bucis bes

bucis: bin
	go build -v -o ./bin/bucis ./cmd/bucis

bes: bin
	go build -v -o ./bin/bes ./cmd/bes

bes-windows: bin
	GOOS=windows GOARCH=amd64 go build -o ./bin/bes.exe ./cmd/bes

clean:
	rm -rf ./bin

# =========================
# Formatting
# =========================

fmt:
	go list -f '{{.Dir}}' ./... | xargs goimports -w

fmt-check:
	@files="$$( go list -f '{{.Dir}}' ./... | xargs goimports -l )"; \
	if [ -n "$$files" ]; then \
		echo "Unformatted files:"; \
		echo "$$files"; \
		exit 1; \
	fi

# =========================
# Lint & analysis
# =========================

lint:
	golangci-lint run --timeout=3m --config .golangci.yml

vet:
	go vet ./...

# =========================
# Tests
# =========================

test:
	go test ./...

test-race-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

bench:
	go test -bench=. -benchmem ./...

# =========================
# Security & deps
# =========================

vulncheck:
	govulncheck ./...

tidy:
	go mod tidy
	@git diff --exit-code -- go.mod go.sum

# =========================
# Pipelines
# =========================

check: fmt-check vet lint test-race-cover vulncheck tidy

ci: check

# =========================
# Run (локально)
# =========================

run-bucis: bucis
	env_file="$${ENV_FILE:-.env}"; case "$$env_file" in /*) ;; *) env_file="./$$env_file";; esac; set -a; [ -f "$$env_file" ] && . "$$env_file"; set +a; exec ./bin/bucis

run-bes: bes
	env_file="$${ENV_FILE:-.env}"; case "$$env_file" in /*) ;; *) env_file="./$$env_file";; esac; set -a; [ -f "$$env_file" ] && . "$$env_file"; set +a; exec ./bin/bes

run-bes-press: bes
	env_file="$${ENV_FILE:-.env}"; case "$$env_file" in /*) ;; *) env_file="./$$env_file";; esac; set -a; [ -f "$$env_file" ] && . "$$env_file"; set +a; exec ./bin/bes --press

opensips:
	-sudo pkill -f "opensips -f deploy/opensips/opensips.cfg"
	sudo opensips -f deploy/opensips/opensips.cfg

opensips-lan:
	-sudo pkill -f "opensips -f deploy/opensips/opensips.lan.cfg"
	sudo opensips -f deploy/opensips/opensips.lan.cfg
