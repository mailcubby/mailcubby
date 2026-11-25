# ADR 0007: License Mailcubby under PolyForm Shield 1.0.0

- Status: Accepted
- Date: 2026-11-25

## Context

NOOP wants the code to stay public, and wants anyone (individuals, companies, agencies) to be able to use it for free, including commercially. It does not want the code reused to launch a competing product or resold in modified form. That restriction must be permanent: licenses that convert to an open source license after a delay (FSL, BSL) were rejected because a stable product would become freely resellable after the delay.

Options considered: MIT (allows reselling), AGPL (allows competing hosted services that publish their changes), FSL-1.1 and BSL-1.1 (convert to open source after 2 to 4 years), Elastic License 2.0 (blocks only hosted services, not resale of a modified binary), PolyForm Noncommercial (blocks agencies and companies, which are the target users), PolyForm Perimeter (blocks all competing use, including internal use instead of a competing tool) and PolyForm Shield.

## Decision

License Mailcubby under the **PolyForm Shield License 1.0.0**, with the notice `Required Notice: Copyright (c) 2026 NOOP` in `LICENSE`.

- Any use is allowed except providing a product that competes with Mailcubby (or another NOOP product that uses it). Using Mailcubby instead of Mailpit or Mailtrap is allowed; offering it to others as an email testing product or service is not.
- Forks and modifications are allowed for permitted purposes.
- The license is perpetual: it never converts to an open source license.
- SPDX does not list PolyForm Shield, so machine-readable fields use `LicenseRef-PolyForm-Shield-1.0.0` (OCI image label). The OpenAPI document links to the license URL.
- Contributions are accepted under an inbound grant in `CONTRIBUTING.md` that lets NOOP license them under PolyForm Shield or any other terms, including commercial licenses.
- The repository history was rewritten before the v1.0.0 release so that every commit carries this license, and the original repository was replaced.

## Consequences

- Mailcubby is described as "source-available", never "open source". Some distributions, package registries and companies that only accept OSI licenses will not include or approve it.
- A future commercial offering (hosted service or paid license) is possible without changing this license.
- Changing the license again needs a new ADR.
