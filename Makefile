.PHONY: dev-up dev-down test vet build sqlc-generate migrate-up migrate-down check-compose logs web-test web-typecheck web-build build-openclaw-runtime verify-openclaw-runtime sync-openclaw-runtime-image debug-ollama debug-newapi newapi-probe seed-e2e

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
	docker compose run --rm --no-deps manager-api go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

web-test:
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm test -- --run"

web-typecheck:
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm run typecheck"

web-build:
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm run build"

build-openclaw-runtime:
	docker build -t openclaw-runtime:dev ./runtime/openclaw

verify-openclaw-runtime:
	./scripts/verify-openclaw-runtime.sh

sync-openclaw-runtime-image:
	./scripts/sync-openclaw-runtime-image.sh

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
