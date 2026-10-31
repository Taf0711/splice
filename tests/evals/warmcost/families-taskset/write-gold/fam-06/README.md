# demo

A small session service. Sessions live in memory and are lost when the process
stops.

## Run

```sh
go run .
```

- `POST /session?id=abc&user=ada` creates a session.
- `GET /session?id=abc` reads one and refreshes its last-seen time.
- `GET /healthz` reports how many sessions are held.

## Test

```sh
go test ./...
```
