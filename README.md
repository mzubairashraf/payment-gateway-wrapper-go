# payment-gateway-wrapper-go

[![CI](https://github.com/mzubairashraf/payment-gateway-wrapper-go/actions/workflows/ci.yml/badge.svg)](https://github.com/mzubairashraf/payment-gateway-wrapper-go/actions/workflows/ci.yml)

One HTTP API for card charges and refunds in front of interchangeable payment gateways. Callers pick a gateway by name; the service hides each provider's wire format and turns their different failure modes into one small, explicit error model.

It ships with an Authorize.Net adapter and an in-memory simulator, uses only the Go standard library, and is about 1,300 lines.

The point of the project is the part of a payment integration that is easy to get wrong: what to do when the gateway does not answer, how to make retries safe when the provider has no idempotency key, and how to keep card data out of logs.

## Quick start

Requires Go 1.25 or newer. No merchant account is needed; the simulator stands in for a gateway.

```bash
git clone https://github.com/mzubairashraf/payment-gateway-wrapper-go && cd payment-gateway-wrapper-go
go test ./...
API_TOKEN=local-dev-token-123456 ENABLE_SIMULATOR=true go run ./cmd/server
```

Charge a card:

```bash
curl -s -X POST http://127.0.0.1:8080/v1/charges \
  -H 'Authorization: Bearer local-dev-token-123456' \
  -H 'Idempotency-Key: order-1001-attempt-1' \
  -d '{"gateway":"simulator","amount":1050,"currency":"USD","card":{"number":"4111111111111111","exp_month":12,"exp_year":2030,"cvv":"123"}}'
```

```json
{"transaction_id":"sim_ch_000001","gateway":"simulator","status":"approved","auth_code":"A00001","amount":1050,"currency":"USD","card":{"brand":"Visa","last4":"1111"}}
```

Run the same command again and the same response comes back with `Idempotent-Replayed: true`; the card is not charged twice.

Simulator test cards: `4111111111111111` approved, `4000000000000002` declined, `4000000000000044` held for review, `4000000000000069` lost response, `4000000000000119` gateway unreachable.

To use the Authorize.Net sandbox, set `AUTHNET_API_LOGIN_ID` and `AUTHNET_TRANSACTION_KEY` from a sandbox account and send `"gateway":"authorizenet"`.

## API

All `/v1` routes require `Authorization: Bearer $API_TOKEN`. Writes require an `Idempotency-Key` header. Amounts are integers in minor units (`1050` is 10.50).

| Route | Purpose |
| --- | --- |
| `POST /v1/charges` | Authorise and capture. Body: `gateway`, `amount`, `currency`, `card{number, exp_month, exp_year, cvv}`, optional `description`, `reference`. |
| `POST /v1/refunds` | Refund all or part of a charge. Body: `gateway`, `transaction_id`, `amount`, `currency`, `card_last4` (required by Authorize.Net). |
| `GET /v1/gateways` | Names of the enabled gateways. |
| `GET /healthz` | Liveness, no authentication. |

The status code alone tells a client what to do next:

| Status | `error.code` | Meaning | Client action |
| --- | --- | --- | --- |
| 201 | | Charged (`approved` or `held_for_review`) or refunded | Store `transaction_id` |
| 400 | `invalid_request` | Malformed request; `error.field` names the field | Fix and resend |
| 402 | `card_declined` | Issuer or gateway declined; no money moved | Ask for another card |
| 422 | `gateway_rejected` | Gateway refused the operation, for example a refund larger than the charge | Do not retry unchanged |
| 503 | `gateway_unavailable` | Could not connect; the request was never delivered | Safe to retry |
| 504 | `outcome_unknown` | Sent, but no usable answer came back | Reconcile before retrying |

Errors look like this:

```json
{"error":{"code":"card_declined","message":"the card was declined: insufficient funds","gateway_code":"insufficient_funds","request_id":"req_c817f4aa307786cc"}}
```

## Configuration

Environment variables only; there is no config file to leak credentials into a repository.

| Variable | Default | Purpose |
| --- | --- | --- |
| `API_TOKEN` | required | Bearer token callers must present; at least 16 characters. |
| `ENABLE_SIMULATOR` | off | Enable the in-memory simulator gateway. |
| `AUTHNET_API_LOGIN_ID`, `AUTHNET_TRANSACTION_KEY` | none | Enable the Authorize.Net gateway. |
| `AUTHNET_ENDPOINT` | sandbox | Authorize.Net API URL; must be https. |
| `AUTHNET_CURRENCY` | `USD` | The merchant account's settlement currency. |
| `GATEWAY_TIMEOUT` | `20s` | Per-request timeout towards a gateway. |
| `ADDR` | `127.0.0.1:8080` | Listen address. |

## Design notes

**One interface, one error type.** A gateway implements three methods (`Name`, `Charge`, `Refund`) and returns a `payment.Error` with one of five kinds. The HTTP layer knows nothing about any provider, and adding a gateway is one new package plus one line in `main`. The `internal/payment` package has no dependency on HTTP or on a gateway.

**"Not delivered" and "no answer" are different failures.** A connection that was refused proves the gateway never saw the charge, so retrying is safe (503). A timeout or a broken response proves nothing: the card may have been charged (504). Most double charges come from treating the second case like the first. The Authorize.Net adapter therefore never retries a transaction on its own, and it distrusts a "success" that carries no transaction id.

**Idempotency at the edge.** Authorize.Net has no idempotency key, so the service provides one. A key runs its request at most once: a repeat replays the stored response, a concurrent duplicate gets 409, and a key reused with a different body gets 422. A request that provably had no effect (400, 503) frees its key so the client can correct or retry; every other result, including 504, is final for that key. The store is in memory, which is correct for one instance; behind a load balancer the same interface would be backed by a shared store.

**Card data.** The `Card` type redacts itself under every `fmt` verb, in `slog`, and in JSON, so a stray log statement cannot print a card number. The access log records method, path, status, duration and request id, never bodies or headers. Tests assert that card numbers, CVVs and the API token do not appear in logs or error messages. Responses carry only the brand and last four digits. Nothing is persisted.

**Provider details handled in the adapter.** Authorize.Net returns HTTP 200 for declines and authentication failures and reports the outcome on two levels in the body; it prefixes its JSON with a byte order mark; and it maps JSON onto an ordered XML schema, so object key order is part of the contract. The adapter handles all three, and a test pins the key order.

**Money.** Integer minor units throughout, formatted for the gateway with integer arithmetic. No floats.

**Testing.** `go test -race ./...` covers the card validation and redaction, the Authorize.Net adapter against an `httptest` server (every response shape above, a timeout, a refused connection), and the HTTP API end to end through the simulator, including twenty concurrent duplicate requests that must produce exactly one charge.

## Limits

- The Authorize.Net adapter implements the `createTransactionRequest` JSON API and is tested against a local fake that reproduces its response shapes. It has not been run against a sandbox merchant account since the rewrite, so treat it as unverified until it has.
- A service that accepts raw card numbers is in PCI DSS scope. In production the browser or app should tokenise the card with the gateway's client library and this API should accept the token; raw card input is kept here because it makes the demo self-contained.
- Authorize.Net refuses refunds of charges that have not settled yet (they must be voided instead). Void is not implemented.
- There is no persistence, so there is no transaction history and idempotency keys do not survive a restart.

## History

The repository dates from 2019. The original was a GOPATH-era sketch with a single working call; it was rewritten in 2026 around the design above. The earlier code remains in the git history.

## Licence

GPL-3.0. See [LICENSE](LICENSE).
