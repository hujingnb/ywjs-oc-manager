.PHONY: dev-up dev-down test vet build migrate-up migrate-down check-compose logs

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

migrate-up:
	docker compose run --rm manager-api go run ./cmd/migrate up

migrate-down:
	docker compose run --rm manager-api go run ./cmd/migrate down

check-compose:
	./scripts/check-compose-bind-mounts.sh

logs:
	docker compose logs -f --tail=200
