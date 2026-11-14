# Copilot Provider Pricing Design

## Goal

Price GitHub Copilot usage with GitHub Copilot's token rates instead of the
underlying model vendor's API rates, while keeping historical promotional
pricing stable and keeping runtime-budget enforcement consistent with the UI.

This remains an estimate. GitHub AI Credit allowances, discounts, and final
`netAmount` reconciliation belong to the separate Billing API work.

## Scope

The first catalog covers the three GPT-5.6 models currently observed on the
dedicated Copilot runtime:

| Model | Period | Default input / cached / cache-write / output | Long-context threshold and rates |
| --- | --- | --- | --- |
| GPT-5.6 Sol | through 2026-09-03 | 2 / 0.2 / 2.5 / 10 | >272K: 4 / 0.4 / 5 / 15 |
| GPT-5.6 Sol | from 2026-09-04 | 4 / 0.4 / 5 / 20 | >272K: 8 / 0.8 / 10 / 30 |
| GPT-5.6 Terra | all dates | 2 / 0.2 / 2.5 / 12 | >272K: 4 / 0.4 / 5 / 18 |
| GPT-5.6 Luna | all dates | 0.2 / 0.02 / 0.25 / 1.2 | >200K: 0.4 / 0.04 / 0.5 / 1.8 |

Rates are USD per million tokens. Sol's 50% promotion ended after September 3,
2026. The effective date is selected from the usage timestamp in UTC, never
from the viewer's current clock or viewing-timezone date.

## Price resolution

Price resolution receives `provider`, `model`, and occurrence date. It tries a
provider-qualified Copilot rule before the existing bare-model catalog:

1. provider-reported `cost_usd_ticks` remains authoritative;
2. `copilot/<canonical-model>` at the usage date;
3. the existing bare model catalog for non-Copilot rows and compatibility;
4. user-supplied custom pricing for otherwise unmapped rows.

The three GPT-5.6 Copilot models are strict provider-scoped SKUs: if their
qualified rule is missing, a Copilot row must not silently borrow the bare
OpenAI API price. Codex/OpenAI rows continue using the existing bare rates.

## Pricing-date-preserving aggregates

Every client-priced aggregate will add `pricing_date`, derived as the UTC date
of `task_usage.created_at` or the UTC hourly bucket. This is intentionally
separate from the existing display `date`, which follows the viewer's timezone:
a viewing day can straddle a UTC pricing boundary. Queries group by the UTC
pricing date before client-side pricing. The UI continues to fold rows by
display date/owner/model/hour after pricing each UTC-dated slice, so the
response change is additive and old clients can ignore the field.

Runtime-budget spend is server-side. Its query will likewise group raw usage by
UTC pricing date instead of pre-summing three periods. Go prices each dated row using
the same Copilot rules, then folds it into every configured daily, weekly, and
monthly window. This prevents the budget gate from disagreeing with the UI
across the Sol promotion boundary.

## Long-context boundary

GitHub applies the threshold to one model request. A Multica task/session delta
can aggregate many model requests, so total task input must not select the long
tier. Both price engines will represent the long-context rules and accept an
optional per-request input size, but production aggregate rows do not provide
that value yet and therefore use the default tier. This is an intentional
lower-bound estimate, not fabricated precision.

Exact long-context pricing requires a future collector change that preserves
request-level `assistant.usage` events or a provider-reported cost. It must not
be inferred from session totals.

## Compatibility and security

- No token or billing credential is added to the pricing path.
- No database migration is required; existing usage timestamps are sufficient.
- API response fields are additive and parsed with defaults for older servers.
- Existing provider-reported costs remain untouched and are never estimated a
  second time.
- Existing custom-price persistence and unrelated provider pricing remain
  unchanged.

## Verification

- Copilot and Codex rows for the same GPT-5.6 model resolve to different rates.
- Sol uses promotional prices on 2026-09-03 and standard prices on 2026-09-04.
- Terra and Luna use GitHub's published cache-write rates.
- A supplied request input size selects the long tier at the exact boundary;
  an aggregate without that signal stays on the default tier.
- Dated by-agent and by-hour rows sum to the daily cost for the same fixtures.
- Runtime budget spend matches the frontend estimate for dated Copilot rows.
