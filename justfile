# Shopping List dev tasks. Run `just` to list.
set dotenv-load := true

container := `command -v podman >/dev/null 2>&1 && echo podman || echo docker`

# list recipes
default:
    @just --list

# run the API locally (needs DATABASE_URL; see `just db`)
run:
    cd backend && go run ./cmd/api

# apply migrations
migrate:
    cd backend && go run ./cmd/migrate up

# revert all migrations
migrate-down:
    cd backend && go run ./cmd/migrate down

# start a local postgres for development
db:
    {{container}} run --name shopping-pg -d --rm \
        -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
        -e POSTGRES_DB=shopping_list -p 5432:5432 postgres:16-alpine
    @echo "DATABASE_URL=postgres://postgres:postgres@localhost:5432/shopping_list?sslmode=disable"

# stop the local postgres
db-stop:
    -{{container}} rm -f shopping-pg

# backend tests
backend-test:
    cd backend && go test ./...

# flutter app tests
app-test:
    cd app && flutter test

# all tests
test: backend-test app-test

# run the flutter app in chrome
app-run:
    cd app && flutter run -d chrome

# build everything
build:
    cd backend && go build ./...
    cd app && flutter build web

# vet backend
vet:
    cd backend && go vet ./...

# tidy backend modules
tidy:
    cd backend && go mod tidy
