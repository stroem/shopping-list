# SAM makefile builder target (used by infra/template.yaml, CodeUri: ../backend).
# SAM copies this module to a scratch dir, runs `make build-ApiFunction` there,
# sets ARTIFACTS_DIR, and packages the `bootstrap` binary it finds.
build-ApiFunction:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		go build -tags lambda.norpc -o "$(ARTIFACTS_DIR)/bootstrap" ./cmd/lambda
