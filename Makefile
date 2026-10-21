.PHONY: test race bench cover check load

test:
	go test ./...

race:
	go test -race ./...

bench:
	go test -run '^$$' -bench . -benchmem ./...

cover:
	go test -cover ./...

check: test race

load:
	go test -tags load -count=1 -v -run Scenario7 ./loadtest/
