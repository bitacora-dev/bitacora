# proto

Protobuf message definitions for the agent-to-hub ingest protocol
(ADR-0008).

- `ingest.proto` — `Batch` (metrics + events + log lines + a ULID
  `batch_id` for idempotency), `IngestResponse`.
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
