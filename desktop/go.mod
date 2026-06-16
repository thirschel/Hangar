// This is intentionally a separate Go module boundary. The desktop app is
// TypeScript/JavaScript and contains no Go we ever build — but some npm
// dependencies (e.g. `flatted`) ship stray .go files under node_modules. Without
// this boundary, the parent `claude-squad` module's `go build ./...` /
// `go test ./...` would descend into desktop/node_modules and pick them up. A
// nested module makes the Go tool skip this whole subtree.
module claude-squad/desktop

go 1.25.0
