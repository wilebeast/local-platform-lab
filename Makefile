SHELL := /bin/bash
PROFILE ?= light

.PHONY: doctor bootstrap deploy verify lab-profiles lab-plan lab-up lab-down lab-status test-lab-profiles deploy-api-bff verify-api-bff deploy-user-service verify-user-service deploy-account-service verify-account-service deploy-local-gateway verify-local-gateway deploy-service-discovery verify-service-discovery verify-service-registration deploy-auth verify-auth verify-bff-session auth-ui rollback argocd-ui destroy generate-user-descriptor generate-account-descriptor ensure-postgres-secrets deploy-webhook-lab verify-webhook-lab deploy-notification-lab verify-notification-lab notification-ui deploy-inbound-webhook-lab verify-inbound-webhook-lab deploy-enterprise-card-lab verify-enterprise-card-lab deploy-xxl-job-lab verify-xxl-job-lab compare-xxl-job-shadow compare-xxl-job-business-shadow verify-xxl-job-recovery cutover-xxl-job verify-xxl-job-cutover verify-xxl-job-retirement-readiness verify-xxl-job-retired xxl-job-ui

doctor:
	@./scripts/doctor.sh

bootstrap:
	@PROFILE="$(PROFILE)" ./scripts/bootstrap.sh

deploy:
	@./scripts/deploy.sh

verify:
	@./scripts/verify.sh

lab-profiles:
	@./scripts/lab-profile.sh list

lab-plan:
	@./scripts/lab-profile.sh plan "$(PROFILE)"

lab-up:
	@./scripts/lab-up.sh "$(PROFILE)"

lab-down:
	@./scripts/lab-profile.sh down

lab-status:
	@./scripts/lab-profile.sh status

test-lab-profiles:
	@bash ./scripts/tests/lab-profile-test.sh

deploy-api-bff:
	@./scripts/deploy-api-bff.sh

verify-api-bff:
	@./scripts/verify-api-bff.sh

deploy-user-service:
	@./scripts/deploy-user-service.sh

verify-user-service:
	@./scripts/verify-user-service.sh

deploy-local-gateway:
	@./scripts/deploy-local-gateway.sh

verify-local-gateway:
	@./scripts/verify-local-gateway.sh

deploy-service-discovery:
	@./scripts/deploy-service-discovery.sh

verify-service-discovery:
	@./scripts/verify-service-discovery.sh

verify-service-registration:
	@./scripts/verify-service-registration.sh

deploy-auth:
	@./scripts/deploy-auth.sh

verify-auth:
	@./scripts/verify-auth.sh

verify-bff-session:
	@./scripts/verify-bff-session.sh

auth-ui:
	@./scripts/auth-ui.sh

rollback:
	@./scripts/rollback.sh

argocd-ui:
	@./scripts/argocd-ui.sh

destroy:
	@./scripts/destroy.sh

generate-user-descriptor:
	@./scripts/generate-user-descriptor.sh

generate-account-descriptor:
	@./scripts/generate-account-descriptor.sh

ensure-postgres-secrets:
	@./scripts/ensure-postgres-secrets.sh

deploy-account-service:
	@./scripts/deploy-account-service.sh

verify-account-service:
	@./scripts/verify-account-service.sh

deploy-webhook-lab:
	@./scripts/deploy-webhook-lab.sh

verify-webhook-lab:
	@./scripts/verify-webhook-lab.sh

deploy-notification-lab:
	@./scripts/deploy-notification-lab.sh

verify-notification-lab:
	@./scripts/verify-notification-lab.sh

notification-ui:
	@./scripts/notification-ui.sh

deploy-inbound-webhook-lab:
	@./scripts/deploy-inbound-webhook-lab.sh

verify-inbound-webhook-lab:
	@./scripts/verify-inbound-webhook-lab.sh

deploy-enterprise-card-lab:
	@./scripts/deploy-enterprise-card-lab.sh

verify-enterprise-card-lab:
	@./scripts/verify-enterprise-card-lab.sh

deploy-xxl-job-lab:
	@./scripts/deploy-xxl-job-lab.sh

verify-xxl-job-lab:
	@./scripts/verify-xxl-job-lab.sh

compare-xxl-job-shadow:
	@./scripts/compare-xxl-job-shadow.sh

compare-xxl-job-business-shadow:
	@./scripts/compare-xxl-job-business-shadow.sh

verify-xxl-job-recovery:
	@./scripts/verify-xxl-job-recovery.sh

cutover-xxl-job:
	@./scripts/cutover-xxl-job.sh

verify-xxl-job-cutover:
	@./scripts/verify-xxl-job-cutover.sh

verify-xxl-job-retirement-readiness:
	@./scripts/verify-xxl-job-retirement-readiness.sh

verify-xxl-job-retired:
	@./scripts/verify-xxl-job-retired.sh

xxl-job-ui:
	@./scripts/xxl-job-ui.sh

.PHONY: deploy-matching-service verify-matching-service
deploy-matching-service:
	@./scripts/deploy-matching-service.sh

verify-matching-service:
	@./scripts/verify-matching-service.sh
