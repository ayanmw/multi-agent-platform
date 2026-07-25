# cron-webhook-ssrf-guard Specification

## ADDED Requirements

### Requirement: Webhook URL scheme whitelist
cron webhook action SHALL accept only URLs with scheme `http` or `https`. Any other scheme SHALL be rejected before the request is initiated.

#### Scenario: Valid HTTPS URL
- **WHEN** cron webhook action receives URL `https://example.com/hook`
- **THEN** the HTTP request is allowed

#### Scenario: File scheme rejected
- **WHEN** cron webhook action receives URL `file:///etc/passwd`
- **THEN** the action fails with an error and no request is made

### Requirement: Private and loopback addresses blocked by default
cron webhook action SHALL reject URLs resolving to loopback, link-local, or private IP addresses unless `CRON_WEBHOOK_ALLOW_PRIVATE` is set to `"true"`. Hostnames that resolve to such addresses SHALL also be rejected.

#### Scenario: Localhost blocked
- **WHEN** cron webhook action receives URL `http://localhost:8080/hook` and `CRON_WEBHOOK_ALLOW_PRIVATE` is unset or `"false"`
- **THEN** the action fails with an error indicating localhost targets are not allowed

#### Scenario: Private IPv4 blocked
- **WHEN** cron webhook action receives URL `http://192.168.1.1/hook` and `CRON_WEBHOOK_ALLOW_PRIVATE` is unset or `"false"`
- **THEN** the action fails with an error indicating private address targets are not allowed

#### Scenario: Private addresses allowed via configuration
- **WHEN** `CRON_WEBHOOK_ALLOW_PRIVATE` is set to `"true"` and the URL points to a private address
- **THEN** the request is allowed
