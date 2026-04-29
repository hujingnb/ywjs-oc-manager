.PHONY: dev-up dev-down test vet build migrate-up migrate-down check-compose logs web-test web-typecheck web-build

dev-up:
	docker compose up -d

dev-down:
	docker compose down

test:
	docker compose run --rm --no-deps manager-api go test ./...

vet:
	docker compose run --rm --no-deps manager-api go vet ./...

build:
	docker compose run --rm --no-deps manager-api go build -o ./tmp/build/server ./cmd/server
	docker compose run --rm --no-deps manager-api go build -o ./tmp/build/migrate ./cmd/migrate

web-test:
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm test -- --run"

web-typecheck:
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm run typecheck"

web-build:
	docker compose run --rm --no-deps manager-web sh -c "npm install && npm run build"

migrate-up:
	docker compose run --rm manager-api go run ./cmd/migrate up

migrate-down:
	docker compose run --rm manager-api go run ./cmd/migrate down

check-compose:
	./scripts/check-compose-bind-mounts.sh

logs:
	docker compose logs -f --tail=200
