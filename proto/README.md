# proto

Protobuf message definitions for the agent-to-hub ingest protocol
(ADR-0008).

- `ingest.proto` — `Batch` (metrics + events + log lines + inventories +
  a ULID `batch_id` for idempotency), `IngestResponse`. `Inventory`
  (ADR-0015) is the wire form of `schema.Inventory` — a declarative list
  snapshot, resent in full each time rather than appended.
- `bitacorapb/` — generated Go code, committed (ADR-0008: "el código
  generado se commitea para que compilar no requiera `protoc`").

## Regenerating

```sh
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.10
protoc --go_out=. --go_opt=module=github.com/bitacora-dev/bitacora proto/ingest.proto
```

Requires `protoc` itself (`brew install protobuf` / `apt install
protobuf-compiler`). Only needed when `ingest.proto` changes — normal
builds use the committed `bitacorapb/ingest.pb.go`.
