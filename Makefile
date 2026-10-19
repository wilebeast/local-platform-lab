SHELL := /bin/bash

.PHONY: doctor bootstrap deploy verify deploy-api-bff verify-api-bff deploy-user-service verify-user-service deploy-local-gateway verify-local-gateway deploy-service-discovery verify-service-discovery verify-service-registration deploy-auth verify-auth auth-ui rollback argocd-ui destroy generate-user-descriptor

doctor:
	@./scripts/doctor.sh

bootstrap:
	@./scripts/bootstrap.sh

deploy:
	@./scripts/deploy.sh

verify:
	@./scripts/verify.sh

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

.PHONY: deploy-matching-service verify-matching-service
deploy-matching-service:
	@./scripts/deploy-matching-service.sh

verify-matching-service:
	@./scripts/verify-matching-service.sh
