build:
	go build -o bin/codetospec ./cmd/codetospec
test:
	go vet ./... && go test ./...
run-fixture:
	go run ./cmd/codetospec run --src testdata/fixture --out /tmp/spec-graph-fixture --facts testdata/fixture/fixture.facts.json
