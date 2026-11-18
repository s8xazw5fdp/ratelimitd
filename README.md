# ratelimitd

Per-tenant token-bucket rate limiting for Go HTTP services. It is a middleware
plus a thin daemon so the limiter can be run in front of an upstream during
rollout.

Why: a single noisy tenant should not be able to spend the capacity of all the
others. Every request is billed to a tenant, each tenant owns an independent
bucket, and exhausted buckets get a `429` with an honest `Retry-After` instead
of queueing. Missing tenant identity fails open: a broken identity layer should
degrade throughput, not take the edge down.

Tokens refill continuously at `rate` per second and are capped at `burst`, so a
tenant that was quiet for five minutes gets `burst` requests, not an unbounded
accumulation.

## Install

```
go build ./cmd/ratelimitd
# or embed the package:
go get github.com/s8xazw5fdp/ratelimitd
```

Requires Go 1.22 (uses the `net/http` method-aware mux and `range` over int).

## Usage

Run the daemon:

```
RL_ADDR=:8080 RL_ADMIN_ADDR=127.0.0.1:8081 ./ratelimitd -rate 5 -burst 10
curl -H 'X-Tenant-ID: acme' localhost:8080/any/path
```

```
200 X-RateLimit-Limit: 10 X-RateLimit-Remaining: 9
200 X-RateLimit-Limit: 10 X-RateLimit-Remaining: 8
429 Retry-After: 1
```

Embed it in an existing server:

```go
limiter, err := ratelimitd.New(ratelimitd.Params{Rate: 20, Burst: 40})
if err != nil {
	return err
}
handler := limiter.Middleware(ratelimitd.DefaultKey)(mux)
```

Swap `DefaultKey` for a `KeyFunc` that reads the tenant from a JWT claim,
`mTLS` CN or a URL prefix. Returning an error from it fails open and marks the
response with `X-RateLimit-Policy: fail-open`.

## Configuration

Flags, with the environment variable that supplies the default:

| Flag | Env | Default | Meaning |
| --- | --- | --- | --- |
| `-addr` | `RL_ADDR` | `:8080` | listener for limited traffic |
| `-admin-addr` | `RL_ADMIN_ADDR` | `127.0.0.1:8081` | admin listener, keep it off the public edge |
| `-rate` | `RL_RATE` | `5` | sustained requests per second per tenant |
| `-burst` | `RL_BURST` | `10` | bucket capacity per tenant |
| `-sweep` | - | `1m` | interval between idle bucket sweeps |
| `-idle` | - | `10m` | idle age after which a bucket is dropped |

`rate` must be positive and `burst` at least 1; invalid values stop the process
on boot rather than silently limiting everything to zero.

## Endpoints

Edge listener: every path goes through the limiter. The bundled demo handler
echoes `tenant` and `path` as JSON so the limits can be exercised with curl;
replace it with the real `http.Handler`.

Admin listener:

- `GET /healthz` - liveness plus the current bucket count.
- `GET /admin/buckets` - JSON map of tenant to remaining tokens.

## Operational notes

- State is in memory and per instance, so limits are `rate / replicas` under
  load balancing unless the edge hashes tenants to instances.
- Buckets are swept every `-sweep` for tenants idle longer than `-idle`;
  bucket count is bounded by tenant churn, not uptime.
- `SIGTERM` drains both listeners with a 10 second budget.
- The limiter is a shedder, not a queue: tune `rate` from measured capacity and
  watch the `429` ratio per tenant before tightening it.

## Layout

```
limiter.go        bucket registry, Allow, sweeper
middleware.go     HTTP middleware, key functions, 429 rendering
errors.go         parameter and key errors
limiter_test.go   refill, isolation, cap and sweep tests
cmd/ratelimitd/   daemon, flags, admin endpoints
```

## TODO

Persist buckets in Postgres so quotas survive a deploy and are shared across
replicas.

## License

MIT, see `LICENSE`.
