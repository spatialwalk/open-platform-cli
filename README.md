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
go run ./cmd/avtkit avatar list
go run ./cmd/avtkit token create app_xxx
```

`avtkit app create` now creates the app and an initial API key in one step, then prints the app name, `app_id`, and `api_key`.

`avtkit avatar list` lists public avatars from the console API using the current login session. By default it truncates long cover URLs to keep table output readable; add `--show-cover-urls` to print full URLs.

Resource list commands also support `ls` aliases: `avtkit app ls`, `avtkit api-key ls`, and `avtkit avatar ls`.

`avtkit token create` creates a temporary session token for an app. It uses the current login session to look up the app's API keys, then calls the existing session token API with the selected API key. By default it uses the first available API key and creates a token valid for 1 hour.

Generated protobuf code is committed under `api/generated`, but the repo does not track protobuf sources under `proto/`.
To refresh generated code locally, check out `shared-proto` next to this repo and run:

```bash
./scripts/protobuf-codegen.sh ../shared-proto
```
