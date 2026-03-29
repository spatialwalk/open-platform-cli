# avtkit

CLI for AvatarKit, implemented in Go.

Auth commands are exposed through `avtkit`:

```bash
go run ./cmd/avtkit login
go run ./cmd/avtkit auth status
go run ./cmd/avtkit auth refresh
go run ./cmd/avtkit logout
go run ./cmd/avtkit app list
go run ./cmd/avtkit app create "demo-app"
go run ./cmd/avtkit app get app_xxx
go run ./cmd/avtkit api-key list app_xxx
go run ./cmd/avtkit api-key create app_xxx
```

Generated protobuf code is committed under `api/generated`, but the repo does not track protobuf sources under `proto/`.
To refresh generated code locally, check out `shared-proto` next to this repo and run:

```bash
./scripts/protobuf-codegen.sh ../shared-proto
```
