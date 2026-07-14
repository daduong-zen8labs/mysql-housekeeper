.PHONY: test race cover lint check integration

test:
	go test ./...

race:
	go test -race ./...

cover:
	go test "-coverprofile=coverage.out" -covermode=atomic -p=1 ./...
	go tool cover -func=coverage.out
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run

check: test lint

integration:
	go test -tags=integration ./internal/mover/ -count=1 -v
