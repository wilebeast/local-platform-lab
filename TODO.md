# Project TODO

## Authentication and gateway security

- [ ] Strengthen APISIX -> BFF request signing and replay protection.
  - [ ] Verify and document the current timestamp acceptance window.
  - [ ] Define one canonical signing string containing HTTP method, normalized path and query, timestamp, request ID, and request body SHA-256.
  - [ ] Add a nonce/request-ID replay cache with a short TTL and atomic single-use semantics.
  - [ ] Reject requests with missing, expired, future, duplicated, or malformed timestamps and nonces.
  - [ ] Reject requests when the signed body hash does not match the received body.
  - [ ] Add regression tests for replay, body tampering, direct BFF access, and the normal APISIX path.
- [ ] Add a Kubernetes NetworkPolicy so only APISIX workloads can reach the BFF service port.
- [ ] Evaluate mTLS between APISIX and BFF after HMAC replay protection and NetworkPolicy are in place.

## Session hardening

- [ ] Track refresh-token families and revoke the whole family when reuse is detected.
- [ ] Add an idle timeout plus an absolute maximum lifetime for device sessions.
- [ ] Bound each refresh-token TTL by the remaining device-session lifetime.
- [ ] Add audit events for login, refresh, logout, device revocation, global version changes, and rejected gateway signatures.
