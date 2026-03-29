# avtkit

CLI for AvatarKit, implemented in Go.

Auth commands are exposed through `avtkit`:

```bash
go run ./cmd/avtkit login
go run ./cmd/avtkit auth status
go run ./cmd/avtkit auth refresh
go run ./cmd/avtkit logout
```

Generated protobuf code is committed under `api/generated`, but the repo does not track protobuf sources under `proto/`.
To refresh generated code locally, check out `shared-proto` next to this repo and run:

```bash
./scripts/protobuf-codegen.sh ../shared-proto
```
