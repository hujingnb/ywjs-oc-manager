.PHONY: dev-up dev-down test vet build sqlc-generate migrate-up migrate-down check-compose logs web-test web-typecheck web-build build-hermes-runtime verify-hermes-runtime sync-hermes-runtime-image debug-ollama debug-newapi newapi-probe seed-e2e smoke-v102 openapi-gen web-types-gen openapi-check

SWAG_VERSION := v2.0.0-rc5
OPENAPI_TS_VERSION := 7.13.0

dev-up:
	docker compose up -d

dev-down:
	docker compose down

test:
	docker compose run --rm --no-deps manager-api go test ./...

integration-test:
	docker compose run --rm \
		-e INTEGRATION_DATABASE_URL=$${INTEGRATION_DATABASE_URL:-postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable} \
		-e INTEGRATION_REDIS_ADDR=$${INTEGRATION_REDIS_ADDR:-manager-redis:6379} \
		manager-api go test -tags=integration ./...

vet:
	docker compose run --rm --no-deps manager-api go vet ./...

build:
	docker compose run --rm --no-deps manager-api go build -o ./tmp/build/server ./cmd/server
	docker compose run --rm --no-deps manager-api go build -o ./tmp/build/migrate ./cmd/migrate
	docker compose run --rm --no-deps manager-api go build -o ./tmp/build/oc-runtime-agent ./runtime/agent

sqlc-generate:
	docker compose run --rm --no-deps manager-api go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate

web-test:
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm test -- --run"

web-typecheck:
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm run typecheck"

web-build:
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm run build"

build-hermes-runtime:
	docker build -t hermes-runtime:dev ./runtime/hermes

verify-hermes-runtime:
	./scripts/verify-hermes-runtime.sh

sync-hermes-runtime-image:
	./scripts/sync-hermes-runtime-image.sh

debug-ollama:
	./scripts/debug-ollama.sh

debug-newapi:
	./scripts/debug-newapi.sh

newapi-probe:
	@bash scripts/newapi-probe.sh

migrate-up:
	docker compose run --rm manager-api go run ./cmd/migrate up

migrate-down:
	docker compose run --rm manager-api go run ./cmd/migrate down

check-compose:
	./scripts/check-compose-bind-mounts.sh

logs:
	docker compose logs -f --tail=200

# seed-e2e：在 manager-api 容器里跑 cmd/seed-e2e，OCM_E2E=1 守门。
# 会 TRUNCATE e2e 业务表后重建 fixture，stdout 末行是 fixture JSON 供 Playwright 解析。
seed-e2e:
	docker compose run --rm -e OCM_E2E=1 manager-api go run ./cmd/seed-e2e

smoke-v102:  ## 跑 v1.0.2 干净环境 smoke（前置：阶段 0 完成）
	@bash scripts/v102-smoke.sh

.PHONY: openapi-gen
openapi-gen: ## 后端注解扫描，覆盖 openapi/openapi.yaml
	go run github.com/swaggo/swag/v2/cmd/swag@$(SWAG_VERSION) init \
		--generalInfo main.go \
		--dir cmd/server,internal/api/handlers,internal/service,internal/domain \
		--output openapi \
		--outputTypes yaml \
		--v3.1
	@mv openapi/swagger.yaml openapi/openapi.yaml

.PHONY: web-types-gen
web-types-gen: ## 前端从 yaml 生成 TypeScript 类型
	cd web && npx openapi-typescript@$(OPENAPI_TS_VERSION) ../openapi/openapi.yaml -o src/api/generated.ts

.PHONY: openapi-check
openapi-check: openapi-gen ## 校验 yaml 是否与代码同步（git 工作区干净才过）
	@git diff --exit-code openapi/openapi.yaml \
		|| (echo "❌ openapi/openapi.yaml 与代码不同步，请跑 make openapi-gen 并 commit"; exit 1)
	@echo "✅ openapi.yaml 与代码同步"
