// Package integration holds the feature-level integration suite for
// 9router-go.
//
// The tests here boot the real HTTP stack — the same chi router, middleware
// chain, handlers, SQLite schema and provider dispatch that the binary serves
// — against a temporary database and a fake LLM upstream. Nothing reaches the
// network: every outbound provider call is intercepted by an httptest server
// seeded through providerConnections.data.baseUrl (AddConnection reads the row
// back and fails if that URL did not persist), and the feature tests never
// start the updater/catalog background loops because they do not boot
// app.ServerModule. See bootfx/ for the test that boots the real fx graph.
//
// The suite is gated behind the "integration" build tag so `go test ./...`
// stays fast; `make test-integration` and the CI `integration` job run it with
// -tags=integration. This file is intentionally untagged so the package always
// has at least one compilable Go file and `go build ./...` / `go vet ./...`
// never hit a directory whose every file is excluded by the tag.
package integration
