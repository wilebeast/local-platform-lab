# Notification Lab

This lab demonstrates a generic notification service in the local Kind cluster. It borrows common architecture principles—separating template rendering, verification-code state, and delivery adapters—without copying product-specific names, schemas, templates, code, or configuration.

## First vertical slice

```text
gRPC client
    |
    v
notification-service
    |-- PostgreSQL: layouts and localized templates
    |-- Redis: code digest, cooldown, failures, temporary block
    `-- SMTP: rendered MIME message
             |
             v
           Mailpit
```

The first phase supports:

- sending a database-backed localized template;
- HTML and attachment MIME parts;
- issuing a cryptographically random six-digit verification code;
- storing only an HMAC digest of that code in Redis;
- cooldown, expiry, failure counting, and temporary blocking;
- atomically consuming a correct code so replay fails;
- health and low-cardinality gRPC request metrics.

Kafka, transactional outbox, retry scheduling, dead-letter handling, and production email providers are deliberately deferred until this synchronous boundary has been verified.

## Deploy and verify

```bash
make deploy-notification-lab
make verify-notification-lab
```

The verification script sends a normal template email, issues a verification code, reads the local message from Mailpit, verifies the code once, and proves that replay is rejected.

To inspect messages manually:

```bash
make notification-ui
```

Then open `http://localhost:18025`. Mailpit captures mail locally; it does not deliver to the public internet and requires no paid email account.

## Data ownership

PostgreSQL is the durable authority for:

- `mail_layouts`: reusable presentation shell;
- `mail_templates`: template key, locale, subject, HTML, and status;
- `mail_delivery_receipts`: reserved for the later delivery-state phase.

Redis stores short-lived security state only. Recipient addresses are fingerprinted in Redis key names, verification codes are HMAC-digested with a runtime pepper, and successful verification deletes the code atomically.

The runtime PostgreSQL DSN and verification-code pepper are generated into a Kubernetes Secret by the deployment script. Their values are not stored in Git.

## Production boundary

Mailpit and the standard-library SMTP adapter are local teaching tools. A production rollout should add a provider adapter, encrypted secret management, transactional outbox, bounded retries, dead-letter inspection, idempotency, suppression rules, delivery receipts, and alerting.
